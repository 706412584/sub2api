package imapi

import (
	"context"
	"strings"
	"sync"
	"time"
)

// BufferConfig tunes one platform's outbound batching. Zero values fall back
// to the defaults in DefaultBufferConfig.
type BufferConfig struct {
	// FlushInterval is how often accumulated text is pushed to the sink.
	FlushInterval time.Duration
	// FlushThreshold pushes early once this many runes accumulate.
	FlushThreshold int
	// MaxChunkRunes is the platform's single-message cap; the current chunk
	// is sealed and rendering continues on a fresh chunk when exceeded.
	MaxChunkRunes int
}

// DefaultBufferConfig matches cc-haha's MessageBuffer defaults.
func DefaultBufferConfig() BufferConfig {
	return BufferConfig{
		FlushInterval:  500 * time.Millisecond,
		FlushThreshold: 200,
		MaxChunkRunes:  4000,
	}
}

// ChunkSink renders the accumulated text of an assistant turn. seq 0 is the
// first post; higher seq values are subsequent edits/continuations of the
// same rendered turn. isFinal marks the terminal render of the turn.
type ChunkSink interface {
	SendChunk(ctx context.Context, seq int, text string, isFinal bool) error
}

// MessageBuffer batches model deltas into platform chunks (cc-haha's
// seal-and-continue pattern): a short turn renders in place by repeatedly
// sending the growing current chunk; when the current chunk exceeds
// MaxChunkRunes it is sealed and a new chunk begins at seq+1.
type MessageBuffer interface {
	Append(delta string)
	Finish(err error)
	// FullText returns everything appended (for persistence). Safe after Finish.
	FullText() string
}

type messageBuffer struct {
	mu       sync.Mutex
	cfg      BufferConfig
	sink     ChunkSink
	ctx      context.Context
	fullText strings.Builder

	pending    string // growing current chunk (what the platform shows now)
	seq        int    // 0 = nothing posted yet
	timer      *time.Timer
	timerArmed bool
	finished   bool
}

// NewMessageBuffer builds a buffer bound to ctx (cancelled ctx fails sinks).
func NewMessageBuffer(ctx context.Context, cfg BufferConfig, sink ChunkSink) MessageBuffer {
	def := DefaultBufferConfig()
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = def.FlushInterval
	}
	if cfg.FlushThreshold <= 0 {
		cfg.FlushThreshold = def.FlushThreshold
	}
	if cfg.MaxChunkRunes <= 0 {
		cfg.MaxChunkRunes = def.MaxChunkRunes
	}
	return &messageBuffer{ctx: ctx, cfg: cfg, sink: sink}
}

// Append accumulates one delta and applies the flush policy.
func (b *messageBuffer) Append(delta string) {
	if delta == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finished {
		return
	}
	b.fullText.WriteString(delta)
	b.pending += delta

	// Seal-and-continue while the current chunk is over the platform cap:
	// one append may carry multiple cap-sized chunks. Only the first
	// cap-sized slice is sealed per iteration; the remainder keeps growing.
	for runeLen(b.pending) >= b.cfg.MaxChunkRunes {
		runes := []rune(b.pending)
		sealed := string(runes[:b.cfg.MaxChunkRunes])
		_ = b.sink.SendChunk(b.ctx, b.seq, sealed, false)
		b.pending = string(runes[b.cfg.MaxChunkRunes:])
		b.seq++
	}

	if runeLen(b.pending) >= b.cfg.FlushThreshold {
		b.sendLocked(false)
		return
	}
	if !b.timerArmed {
		b.timer = time.AfterFunc(b.cfg.FlushInterval, b.timerFlush)
		b.timerArmed = true
	}
}

func (b *messageBuffer) timerFlush() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.timerArmed = false
	if b.finished || b.pending == "" {
		return
	}
	b.sendLocked(false)
}

// sendLocked pushes the growing current chunk (in-place render). Must hold mu.
func (b *messageBuffer) sendLocked(isFinal bool) {
	if b.pending == "" && b.seq == 0 {
		return
	}
	_ = b.sink.SendChunk(b.ctx, b.seq, b.pending, isFinal)
}

// Finish flushes the remainder as the terminal render and closes the turn.
func (b *messageBuffer) Finish(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finished {
		return
	}
	b.finished = true
	if b.timerArmed {
		b.timer.Stop()
		b.timerArmed = false
	}
	if b.pending != "" {
		_ = b.sink.SendChunk(b.ctx, b.seq, b.pending, true)
	} else if b.seq == 0 {
		// Turn produced no text (upstream error before content): let the
		// platform render an empty final state.
		_ = b.sink.SendChunk(b.ctx, 0, "", true)
	}
}

// FullText returns everything appended (for persistence). Safe after Finish.
func (b *messageBuffer) FullText() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.fullText.String()
}

func runeLen(s string) int { return len([]rune(s)) }
