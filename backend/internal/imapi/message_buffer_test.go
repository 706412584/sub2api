package imapi

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// recordingSink records chunk renders.
type recordingSink struct {
	mu     sync.Mutex
	chunks []string
	finals []bool
}

func (r *recordingSink) SendChunk(_ context.Context, seq int, text string, isFinal bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.chunks) <= seq {
		r.chunks = append(r.chunks, "")
		r.finals = append(r.finals, false)
	}
	r.chunks[seq] = text
	if isFinal {
		r.finals[seq] = true
	}
	return nil
}

func (r *recordingSink) snapshot() ([]string, []bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := append([]string(nil), r.chunks...)
	fi := append([]bool(nil), r.finals...)
	return ch, fi
}

func TestMessageBuffer_ThresholdFlush(t *testing.T) {
	sink := &recordingSink{}
	b := NewMessageBuffer(context.Background(), BufferConfig{
		FlushThreshold: 5, FlushInterval: time.Hour, // no time flush
	}, sink)
	b.Append("abcdef") // >= threshold -> immediate flush
	chunks, _ := sink.snapshot()
	require.Len(t, chunks, 1)
	require.Equal(t, "abcdef", chunks[0])
	require.False(t, chunks[1-1] == "" && len(chunks) > 0 && false)
}

func TestMessageBuffer_SealAndContinue(t *testing.T) {
	sink := &recordingSink{}
	b := NewMessageBuffer(context.Background(), BufferConfig{
		FlushThreshold: 1000,      // never early-flush
		FlushInterval: time.Hour, // no timer flush
		MaxChunkRunes:  10,
	}, sink)
	b.Append("0123456789")   // exactly cap -> seal chunk 0
	b.Append("abcdefgh")    // pending in chunk 1
	b.Finish(nil)           // final flush chunk 1
	chunks, finals := sink.snapshot()
	require.Len(t, chunks, 2)
	require.Equal(t, "0123456789", chunks[0])
	require.False(t, finals[0]) // sealed chunk is not the final render
	require.Equal(t, "abcdefgh", chunks[1])
	require.True(t, finals[1])
	require.Equal(t, "0123456789abcdefgh", b.FullText())
}

func TestMessageBuffer_TimeFlush(t *testing.T) {
	sink := &recordingSink{}
	b := NewMessageBuffer(context.Background(), BufferConfig{
		FlushThreshold: 1000,
		FlushInterval:  20 * time.Millisecond,
	}, sink)
	b.Append("x")
	chunks, _ := sink.snapshot()
	require.Empty(t, chunks) // not yet flushed
	time.Sleep(60 * time.Millisecond)
	chunks, _ = sink.snapshot()
	require.Len(t, chunks, 1)
	require.Equal(t, "x", chunks[0])
	b.Finish(nil)
	_, finals := sink.snapshot()
	require.True(t, finals[0])
}

func TestMessageBuffer_GrowingChunk(t *testing.T) {
	// Mid-turn flushes send the GROWING current chunk (typewriter edit), not deltas.
	sink := &recordingSink{}
	b := NewMessageBuffer(context.Background(), BufferConfig{
		FlushThreshold: 3,
		FlushInterval:  time.Hour,
	}, sink)
	b.Append("ab")
	b.Append("cd") // threshold 3 hit at 4 runes -> flush "abcd"
	b.Append("e")
	b.Finish(nil)
	chunks, finals := sink.snapshot()
	require.Len(t, chunks, 1)
	require.Equal(t, "abcde", chunks[0])
	require.True(t, finals[0])
}

func TestMessageBuffer_EmptyTurn(t *testing.T) {
	sink := &recordingSink{}
	b := NewMessageBuffer(context.Background(), DefaultBufferConfig(), sink)
	b.Finish(nil) // upstream error before any content
	chunks, finals := sink.snapshot()
	require.Len(t, chunks, 1)
	require.Empty(t, chunks[0])
	require.True(t, finals[0])
}

func TestMessageBuffer_MultibyteRunes(t *testing.T) {
	sink := &recordingSink{}
	b := NewMessageBuffer(context.Background(), BufferConfig{
		FlushThreshold: 1000,
		FlushInterval:  time.Hour,
		MaxChunkRunes:  3,
	}, sink)
	b.Append("中文内容测试") // 6 runes -> seal at 3, rest pending
	b.Finish(nil)
	chunks, _ := sink.snapshot()
	require.Len(t, chunks, 2)
	require.Equal(t, "中文内", chunks[0])
	require.Equal(t, "容测试", chunks[1])
	require.NotContains(t, strings.Join(chunks, ""), "\ufffd")
}
