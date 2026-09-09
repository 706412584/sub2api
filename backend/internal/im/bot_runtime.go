package im

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

// botHandle is one running bot: its adapter, its supervisor loop, and the
// per-chat serialized processing queues. All platform SDK callbacks funnel
// into handleInbound, which enqueues per chat so one window's messages are
// processed strictly in order (cc-haha lesson: async handlers interleave at
// await points and replies arrive out of order).
type botHandle struct {
	hub     *Hub
	bot     *service.IMBot
	adapter PlatformAdapter

	supCtx    context.Context
	supCancel context.CancelFunc
	// restart bookkeeping
	mu             sync.Mutex
	backoff        int
	connectedSince time.Time
	lastError      string

	// per-chat FIFO: one worker goroutine per chat, tasks pushed to a queue.
	chatMu   sync.Mutex
	queues   map[string]chan inboundTask
	cancels  map[string]context.CancelFunc // active turn cancel per chat
	dedup    sync.Map                      // rawMessageID -> struct{} (memory fallback)
	inflight chan struct{}                 // global turn semaphore
	done     chan struct{}
}

type inboundTask struct {
	ctx context.Context
	msg InboundMessage
}

const (
	perChatQueueSize     = 16
	supervisorMaxBackoff = 5
)

func newBotHandle(h *Hub, bot *service.IMBot, adapter PlatformAdapter) *botHandle {
	Concurrency := bot.MaxConcurrency
	if Concurrency < 1 {
		Concurrency = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &botHandle{
		hub:       h,
		bot:       bot,
		adapter:   adapter,
		supCtx:    ctx,
		supCancel: cancel,
		queues:    map[string]chan inboundTask{},
		cancels:   map[string]context.CancelFunc{},
		inflight:  make(chan struct{}, Concurrency),
		done:      make(chan struct{}),
	}
}

// supervise runs the adapter loop with panic isolation and backoff.
func (b *botHandle) supervise(parent context.Context) {
	go func() {
		defer close(b.done)
		for {
			select {
			case <-b.supCtx.Done():
				return
			case <-parent.Done():
				return
			default:
			}

			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.L().Error("im.adapter panic recovered",
							zap.Int64("bot_id", b.bot.ID),
							zap.Any("panic", r))
						b.mu.Lock()
						b.lastError = fmt.Sprintf("adapter panic: %v", r)
						b.mu.Unlock()
					}
				}()
				b.mu.Lock()
				b.connectedSince = time.Now()
				b.lastError = ""
				b.mu.Unlock()
				err := b.adapter.Run(b.supCtx, b.handleInbound)
				if err != nil {
					b.mu.Lock()
					b.lastError = err.Error()
					b.mu.Unlock()
					logger.L().Warn("im.adapter loop exited",
						zap.Int64("bot_id", b.bot.ID), zap.Error(err))
				}
			}()

			// Exited (error or panic): back off and retry, or give up.
			b.mu.Lock()
			b.backoff++
			n := b.backoff
			b.mu.Unlock()
			if n > supervisorMaxBackoff {
				logger.L().Error("im.adapter gave up after repeated failures",
					zap.Int64("bot_id", b.bot.ID))
				_ = b.hub.repo.UpdateStatus(context.Background(),
					b.bot.ID, service.IMBotStatusError, b.lastError)
				return
			}
			wait := time.Duration(1<<uint(n-1)) * time.Second // 1s,2s,4s,8s,16s
			select {
			case <-time.After(wait):
			case <-b.supCtx.Done():
				return
			case <-parent.Done():
				return
			}
		}
	}()
}

func (b *botHandle) running() bool {
	select {
	case <-b.done:
		return false
	default:
		return true
	}
}

func (b *botHandle) shutdown() {
	// stop accepting, cancel adapter loop, drain queues
	b.supCancel()
	// cancel active turns
	b.chatMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(b.cancels))
	for _, c := range b.cancels {
		cancels = append(cancels, c)
	}
	b.chatMu.Unlock()
	for _, c := range cancels {
		c()
	}
	select {
	case <-b.done:
	case <-time.After(5 * time.Second):
	}
}

// handleInbound is the adapter's callback target.
func (b *botHandle) handleInbound(_ context.Context, msg InboundMessage) {
	// memory-level dedup (redis SETNX added in enqueue path via hub.rdb)
	if _, loaded := b.dedup.LoadOrStore(msg.RawMessageID, struct{}{}); loaded {
		return
	}
	b.chatMu.Lock()
	q, ok := b.queues[msg.ChatID]
	if !ok {
		q = make(chan inboundTask, perChatQueueSize)
		b.queues[msg.ChatID] = q
		go b.chatWorker(q)
	}
	b.chatMu.Unlock()
	select {
	case q <- inboundTask{ctx: b.supCtx, msg: msg}:
	default:
		// Queue full: drop with a notice attempt (non-blocking).
		logger.L().Warn("im.chat queue full, message dropped",
			zap.Int64("bot_id", msg.BotID), zap.String("chat_id", msg.ChatID))
	}
}

// chatWorker serially drains one chat's queue.
func (b *botHandle) chatWorker(q chan inboundTask) {
	for task := range q {
		b.processMessage(task.ctx, task.msg)
	}
}

// stopCurrentTurn cancels the chat's in-flight generation (/stop).
func (b *botHandle) stopCurrentTurn(chatID string) {
	b.chatMu.Lock()
	c := b.cancels[chatID]
	b.chatMu.Unlock()
	if c != nil {
		c()
	}
}

// processMessage runs the full inbound pipeline for one message:
// gate -> command -> forward -> render -> persist.
func (b *botHandle) processMessage(parent context.Context, msg InboundMessage) {
	if strings.TrimSpace(msg.Text) == "" {
		return
	}
	port := b.adapter.ChatPort()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	b.chatMu.Lock()
	b.cancels[msg.ChatID] = cancel
	b.chatMu.Unlock()
	defer func() {
		b.chatMu.Lock()
		delete(b.cancels, msg.ChatID)
		b.chatMu.Unlock()
	}()

	// ---- gate: paired chat or pairing attempt ----
	chat, err := b.hub.repo.GetChatByBotAndChatID(ctx, b.bot.ID, msg.ChatID)
	if err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "not found") {
			logger.L().Warn("im.chat lookup failed", zap.Int64("bot_id", b.bot.ID), zap.Error(err))
			return
		}
		chat = nil
	}
	if chat != nil && chat.Status == service.IMChatStatusBlocked {
		return
	}
	if chat == nil {
		if !b.bot.PairingEnabled {
			if b.hub.pairing.DenyHintAllowed(ctx, fmt.Sprintf("%d:%s", b.bot.ID, msg.ChatID)) {
				_ = port.SendNotice(ctx, msg.ChatID,
					"⛔ 该机器人未开放配对，请联系管理员。")
			}
			return
		}
		// treat the text as a pairing code attempt
		chat, err = b.hub.pairing.Verify(ctx, b.hub.repo, b.bot, msg.Text, msg.PlatformUser, msg.ChatID, msg.DisplayName)
		if err != nil {
			if b.hub.pairing.DenyHintAllowed(ctx, fmt.Sprintf("%d:%s", b.bot.ID, msg.ChatID)) {
				if strings.Contains(err.Error(), "rate limited") {
					_ = port.SendNotice(ctx, msg.ChatID, "⏳ 尝试过于频繁，请几分钟后再试。")
				} else {
					_ = port.SendNotice(ctx, msg.ChatID,
						"🔐 首次使用请先发送管理员提供的 6 位配对码。")
				}
			}
			return
		}
		_ = port.SendNotice(ctx, msg.ChatID, "✅ 配对成功，开始聊天吧！发送 /help 查看命令。")
	}

	// ---- commands ----
	res, err := HandleCommand(ctx, msg.Text, b.bot, chat, CommandDeps{
		Repo:            b.hub.repo,
		StopCurrentTurn: b.stopCurrentTurn,
		EffectiveModel:  b.effectiveModel,
		MaxHistoryBytes: b.hub.cfg.MaxHistoryBytes,
	})
	if err != nil {
		logger.L().Warn("im.command failed", zap.Int64("bot_id", b.bot.ID), zap.Error(err))
		_ = port.SendNotice(ctx, msg.ChatID, "⚠️ 命令执行失败，请稍后再试。")
		return
	}
	if res.Handled {
		if res.Reply != "" {
			_ = port.SendNotice(ctx, msg.ChatID, res.Reply)
		}
		return
	}

	// ---- inbound limits + persist user turn ----
	if len(msg.Text) > b.hub.cfg.MaxInboundBytes {
		_ = port.SendNotice(ctx, msg.ChatID, "⚠️ 消息过长，请缩短后重试。")
		return
	}
	if err := b.hub.repo.AppendMessage(ctx, &service.IMBotMessage{
		ChatID: chat.ID, BotID: b.bot.ID,
		Role: service.IMMessageRoleUser, Content: msg.Text,
	}); err != nil {
		logger.L().Warn("im.persist user message failed", zap.Int64("chat_id", chat.ID), zap.Error(err))
	}

	// ---- forward through the gateway ----
	b.inflight <- struct{}{}
	defer func() { <-b.inflight }()

	key, err := b.hub.keyRepo.GetByID(ctx, b.bot.APIKeyID)
	if err != nil || key == nil {
		_ = port.SendNotice(ctx, msg.ChatID, "⚠️ 绑定的 API Key 不可用，请联系管理员。")
		return
	}

	msgs, err := b.hub.repo.ListRecentMessages(ctx, chat.ID, b.effectiveHistoryMax()+1)
	if err != nil {
		logger.L().Warn("im.history load failed", zap.Int64("chat_id", chat.ID), zap.Error(err))
	}
	window := BuildHistoryWindow(msgs, b.effectiveHistoryMax(), b.hub.cfg.MaxHistoryBytes)

	stream := port.CreateStream(ctx, msg.ChatID)
	fwdReq := &ForwardRequest{
		APIKey:        key.Key,
		Model:         b.effectiveModel(chat),
		System:        b.bot.SystemPrompt,
		Messages:      window,
		SessionUUID:   chat.SessionUUID,
		SessionLogKey: SessionUUIDKey(b.bot.ID, chat.SessionUUID),
		DeviceHex64:   DeviceHexFor(b.bot.ID, b.bot.Platform),
		MaxTokens:     b.hub.cfg.DefaultMaxTokens,
	}
	result, fwdErr := b.hub.fwd.Stream(ctx, fwdReq, stream)

	// ---- persist assistant turn ----
	if result != nil && strings.TrimSpace(result.FullText) != "" {
		_ = b.hub.repo.AppendMessage(context.Background(), &service.IMBotMessage{
			ChatID: chat.ID, BotID: b.bot.ID,
			Role:      service.IMMessageRoleAssistant,
			Content:   result.FullText,
			RequestID: result.RequestID,
		})
	}
	if fwdErr != nil {
		logger.L().Warn("im.forward error",
			zap.Int64("bot_id", b.bot.ID), zap.String("chat_id", msg.ChatID), zap.Error(fwdErr))
		// The stream already surfaced the error; add a notice for hard failures.
		if strings.Contains(fwdErr.Error(), "status 401") || strings.Contains(fwdErr.Error(), "status 403") {
			_ = port.SendNotice(ctx, msg.ChatID, "⚠️ 绑定的 API Key 已失效或无权限，请联系管理员。")
		}
	}
}

func (b *botHandle) effectiveModel(chat *service.IMBotChat) string {
	if chat != nil && chat.ModelOverride != "" {
		return chat.ModelOverride
	}
	if b.bot.ModelOverride != "" {
		return b.bot.ModelOverride
	}
	return ""
}

func (b *botHandle) effectiveHistoryMax() int {
	if b.bot.HistoryMaxMessages > 0 {
		return b.bot.HistoryMaxMessages
	}
	return 40
}
