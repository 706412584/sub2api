package im

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/imapi"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Hub owns all running bot adapters: lifecycle (start/stop/reload), panic
// isolation (one bot's SDK crash never takes down the hub or gateway), a
// Redis leader lock for multi-instance deployments, and a daily retention
// sweep for chat messages.
type Hub struct {
	cfg       config.IMConfig
	repo      service.IMBotRepository
	keyRepo   APIKeySource
	enc       service.SecretEncryptor
	rdb       redis.Cmdable
	fwd       *Forwarder
	factories map[Platform]AdapterFactory

	mu      sync.Mutex
	bots    map[int64]*botHandle
	leader  bool
	stopped bool

	pairing *pairing
}

// APIKeySource is the narrow slice of APIKeyRepository the hub needs:
// resolving the bound key's plaintext for gateway self-calls.
type APIKeySource interface {
	GetByID(ctx context.Context, id int64) (*service.APIKey, error)
}

// NewHub builds the IM hub. Call Start to run enabled bots.
func NewHub(cfg config.IMConfig, repo service.IMBotRepository, keyRepo APIKeySource,
	enc service.SecretEncryptor, rdb redis.Cmdable, factories []AdapterFactory) *Hub {
	h := &Hub{
		cfg:     cfg,
		repo:    repo,
		keyRepo: keyRepo,
		enc:     enc,
		rdb:     rdb,
		fwd:     NewForwarder(cfg.BaseURL),
		bots:    map[int64]*botHandle{},
		factories: map[Platform]AdapterFactory{},
	}
	if h.cfg.BaseURL == "" {
		h.cfg.BaseURL = "http://127.0.0.1:18080"
	}
	for _, f := range factories {
		h.factories[f.Platform()] = f
	}
	h.pairing = newPairing(rdb, time.Duration(cfg.PairingCodeTTLSeconds)*time.Second)
	return h
}

// Factory returns the registered adapter factory for a platform (nil if none).
func (h *Hub) Factory(p Platform) AdapterFactory { return h.factories[p] }

// Start becomes leader (if possible) and boots every enabled bot.
func (h *Hub) Start(ctx context.Context) error {
	if !h.cfg.Enabled {
		logger.L().Info("im.hub disabled by config")
		return nil
	}
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return errors.New("im hub already stopped")
	}
	h.mu.Unlock()

	h.leader = h.tryAcquireLeader(ctx)
	if !h.leader {
		logger.L().Info("im.hub another instance holds the leader lock; adapters not started here")
	}

	bots, err := h.repo.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("im.hub list enabled bots: %w", err)
	}
	started := 0
	for i := range bots {
		if !h.leader {
			break
		}
		if err := h.startBotLocked(ctx, &bots[i]); err != nil {
			logger.L().Warn("im.hub bot failed to start", zap.Int64("bot_id", bots[i].ID), zap.Error(err))
			_ = h.repo.UpdateStatus(context.Background(), bots[i].ID, service.IMBotStatusError, err.Error())
			continue
		}
		started++
	}
	logger.L().Info("im.hub started", zap.Int("bots", started), zap.Bool("leader", h.leader))

	go h.leaderLoop(ctx)
	go h.retentionLoop(ctx)
	return nil
}

// Stop tears every adapter down (registered in cmd/server cleanup).
func (h *Hub) Stop() {
	h.mu.Lock()
	h.stopped = true
	handles := make([]*botHandle, 0, len(h.bots))
	for _, bh := range h.bots {
		handles = append(handles, bh)
	}
	h.bots = map[int64]*botHandle{}
	h.mu.Unlock()
	for _, bh := range handles {
		bh.shutdown()
	}
}

// ReloadBot re-creates the adapter after an admin update/enable. Safe on a
// non-leader instance (no adapter to stop; starts only if leader).
func (h *Hub) ReloadBot(ctx context.Context, botID int64) error {
	if !h.cfg.Enabled {
		return nil
	}
	bot, err := h.repo.GetByID(ctx, botID)
	if err != nil {
		return err
	}
	h.stopBot(botID)
	if bot.Status != service.IMBotStatusEnabled {
		return nil
	}
	if !h.leader {
		return nil
	}
	return h.startBotLocked(ctx, bot)
}

// StopBot tears one adapter down after disable/delete.
func (h *Hub) StopBot(ctx context.Context, botID int64) error {
	h.stopBot(botID)
	return nil
}

// GeneratePairCode issues a fresh pairing code for a bot (admin action) and,
// when the platform exposes a public bot handle, the scan-to-pair deep link
// (e.g. https://t.me/<bot>?start=<code>) for QR rendering.
func (h *Hub) GeneratePairCode(ctx context.Context, botID int64) (string, time.Time, string, error) {
	code, expiresAt, err := h.pairing.Generate(ctx, botID)
	if err != nil {
		return "", time.Time{}, "", err
	}
	link := ""
	h.mu.Lock()
	bh := h.bots[botID]
	h.mu.Unlock()
	if bh != nil {
		if pp, ok := bh.adapter.(imapi.ProfileProvider); ok {
			if username, perr := pp.BotUsername(ctx); perr == nil && username != "" {
				switch Platform(bh.bot.Platform) {
				case imapi.PlatformTelegram:
					link = fmt.Sprintf("https://t.me/%s?start=%s", username, code)
				}
			}
		}
	}
	return code, expiresAt, link, nil
}

// RuntimeStatus implements service.IMBotReloader.
func (h *Hub) RuntimeStatus(ctx context.Context, botID int64) (*service.IMBotRuntimeStatus, error) {
	h.mu.Lock()
	bh := h.bots[botID]
	leader := h.leader
	h.mu.Unlock()
	st := &service.IMBotRuntimeStatus{BotID: botID, IsLeader: leader}
	if bh == nil {
		return st, nil
	}
	st.Running = bh.running()
	st.LastError = bh.lastError
	t := bh.connectedSince
	st.ConnectedSince = &t
	return st, nil
}

// startBotLocked builds the handle and launches its supervisor goroutine.
func (h *Hub) startBotLocked(ctx context.Context, bot *service.IMBot) error {
	factory := h.factories[Platform(bot.Platform)]
	if factory == nil {
		return fmt.Errorf("no adapter factory for platform %q", bot.Platform)
	}
	creds, err := h.enc.Decrypt(bot.CredentialsEnc)
	if err != nil {
		return fmt.Errorf("decrypt credentials: %w", err)
	}
	adapter, err := factory.Create(BotConfig{
		ID:                 bot.ID,
		Name:               bot.Name,
		Platform:           Platform(bot.Platform),
		ModelOverride:      bot.ModelOverride,
		SystemPrompt:       bot.SystemPrompt,
		MaxConcurrency:     bot.MaxConcurrency,
		HistoryMaxMessages: bot.HistoryMaxMessages,
	}, []byte(creds))
	if err != nil {
		return err
	}
	bh := newBotHandle(h, bot, adapter)
	h.mu.Lock()
	h.bots[bot.ID] = bh
	h.mu.Unlock()
	bh.supervise(ctx)
	return nil
}

func (h *Hub) stopBot(botID int64) {
	h.mu.Lock()
	bh := h.bots[botID]
	delete(h.bots, botID)
	h.mu.Unlock()
	if bh != nil {
		bh.shutdown()
	}
}

// leaderLoop keeps the leader lock renewed while running as leader.
func (h *Hub) leaderLoop(ctx context.Context) {
	if !h.leader {
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !h.renewLeader(ctx) {
				h.leader = false
				logger.L().Warn("im.hub lost leader lock; stopping adapters")
				h.mu.Lock()
				handles := make([]*botHandle, 0, len(h.bots))
				for _, bh := range h.bots {
					handles = append(handles, bh)
				}
				h.bots = map[int64]*botHandle{}
				h.mu.Unlock()
				for _, bh := range handles {
					bh.shutdown()
				}
				return
			}
		}
	}
}

const imLeaderKey = "im:hub:leader"

func (h *Hub) tryAcquireLeader(ctx context.Context) bool {
	ok, err := h.rdb.SetNX(ctx, imLeaderKey, "1", 2*time.Minute).Result()
	if err != nil {
		logger.L().Warn("im.hub leader SetNX failed; assuming follower to be safe", zap.Error(err))
		return false
	}
	return ok
}

func (h *Hub) renewLeader(ctx context.Context) bool {
	return h.rdb.Expire(ctx, imLeaderKey, 2*time.Minute).Err() == nil
}

// retentionLoop sweeps old chat messages once a day.
func (h *Hub) retentionLoop(ctx context.Context) {
	interval := 24 * time.Hour
	if h.cfg.MessageRetentionDays <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			before := time.Now().AddDate(0, 0, -h.cfg.MessageRetentionDays)
			n, err := h.repo.SweepExpiredMessages(context.Background(), before)
			if err != nil {
				logger.L().Warn("im.hub retention sweep failed", zap.Error(err))
				continue
			}
			if n > 0 {
				logger.L().Info("im.hub retention sweep", zap.Int64("deleted", n))
			}
		}
	}
}

// DeviceHexFor returns the bot's stable 64-hex device component of
// metadata.user_id (sticky scheduling bucket).
func DeviceHexFor(botID int64, platform string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("im:%s:%d", platform, botID)))
	return hex.EncodeToString(sum[:])
}

// Compile-time: the hub satisfies the admin-side reloader contract.
var _ service.IMBotReloader = (*Hub)(nil)
