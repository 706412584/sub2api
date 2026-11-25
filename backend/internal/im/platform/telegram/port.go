package telegram

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/Wei-Shaw/sub2api/internal/imapi"
)

// Port renders assistant turns as typewriter-edited messages. A turn starts
// with a placeholder, deltas edit it in place, and once it exceeds
// streamMaxChars the current message is sealed and a new placeholder opens
// (seal-and-continue, cc-haha pattern).
type Port struct {
	bot *tgbotapi.BotAPI
}

// SendNotice implements im.ChatPort (non-streamed command output / errors).
func (p *Port) SendNotice(ctx context.Context, chatID, text string) error {
	cid, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("telegram chat id: %w", err)
	}
	msg := tgbotapi.NewMessage(cid, text)
	msg.ParseMode = ""
	return p.sendWithRetry(ctx, msg)
}

// CreateStream implements im.ChatPort.
func (p *Port) CreateStream(ctx context.Context, chatID string) imapi.OutboundStream {
	s := &stream{port: p, chatID: chatID}
	s.buf = imapi.NewMessageBuffer(ctx, imapi.BufferConfig{
		FlushInterval:  flushInterval,
		FlushThreshold: flushChars,
		MaxChunkRunes:  streamMaxChars,
	}, s)
	return s
}

// sendWithRetry posts a message honoring Telegram 429 retry_after.
func (p *Port) sendWithRetry(ctx context.Context, msg tgbotapi.Chattable) error {
	for attempt := 0; attempt < 3; attempt++ { //nolint:mnd // bounded retries
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := p.bot.Send(msg)
		if err == nil {
			return nil
		}
		if api, ok := err.(tgbotapi.Error); ok && api.RetryAfter > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(api.RetryAfter) * time.Second):
			}
			continue
		}
		return err
	}
	return fmt.Errorf("telegram send: retries exhausted")
}

// editWithRetry updates an existing message honoring 429.
func (p *Port) editWithRetry(ctx context.Context, chatID, messageID, text string) error {
	cid, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return err
	}
	mid, _ := strconv.Atoi(messageID)
	edit := tgbotapi.NewEditMessageText(cid, mid, text)
	for attempt := 0; attempt < 3; attempt++ { //nolint:mnd // bounded retries
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := p.bot.Send(edit)
		if err == nil {
			return nil
		}
		if api, ok := err.(tgbotapi.Error); ok && api.RetryAfter > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(api.RetryAfter) * time.Second):
			}
			continue
		}
		return err
	}
	return fmt.Errorf("telegram edit: retries exhausted")
}

// stream is one turn's OutboundStream + ChunkSink.
type stream struct {
	port      *Port
	chatID    string
	buf       imapi.MessageBuffer
	mu        sync.Mutex
	curMsgID  string
	curLen    int
	finalText string
	done      chan struct{}
	once      sync.Once
	finErr    error
}

// SendChunk implements imapi.ChunkSink: seq 0 posts a placeholder (returns its
// message id as the edit anchor), seq>0 edits in place. Seal-and-continue is
// decided by the buffer: a new seq sequence follows a sealed chunk.
func (s *stream) SendChunk(ctx context.Context, seq int, text string, isFinal bool) error {
	if seq == 0 {
		cid, err := strconv.ParseInt(s.chatID, 10, 64)
		if err != nil {
			return err
		}
		placeholder := "▍"
		msg := tgbotapi.NewMessage(cid, placeholder)
		sent, err := s.port.bot.Send(msg)
		if err != nil {
			return err
		}
		s.curMsgID = fmt.Sprintf("%d", sent.MessageID)
		s.curLen = len(placeholder)
		return nil
	}
	if err := s.port.editWithRetry(ctx, s.chatID, s.curMsgID, text); err != nil {
		return err
	}
	s.curLen = len(text)
	return nil
}

// ChunkPlan asks the buffer whether the pending text still fits the current
// message; when it would exceed streamMaxChars the buffer seals and reopens
// with seq=0 handled above. It is consulted through the sink interface only,
// so expose the limit decision via MaxChunkChars at construction time.
var _ = strconv.Atoi

// Append implements im.OutboundStream.
func (s *stream) Append(delta string) {
	s.mu.Lock()
	s.finalText += delta
	s.mu.Unlock()
	s.buf.Append(delta)
}

// Finish implements im.OutboundStream: flush the tail and mark complete.
func (s *stream) Finish(err error) {
	s.once.Do(func() {
		s.finErr = err
		s.buf.Finish(err)
		if s.done != nil {
			close(s.done)
		}
	})
}

// Done exposes completion for the runtime if needed.
func (s *stream) Done() <-chan struct{} {
	if s.done == nil {
		s.done = make(chan struct{})
	}
	return s.done
}

// FullText returns everything appended (for message persistence).
func (s *stream) FullText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finalText
}
