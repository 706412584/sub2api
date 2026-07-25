package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/robfig/cron/v3"
)

const (
	accountPoolProbeExtraKey          = "account_pool_probe_at"
	accountPoolProbeDefaultTickCron   = "*/5 * * * *" // check settings every 5 minutes
	accountPoolProbeRunTimeout        = 4 * time.Minute
	accountPoolProbeSingleTimeout     = 45 * time.Second
)

// AccountPoolProbeRunner periodically probes a small batch of openai/grok accounts
// off the request path. Manual probes are never blocked by this runner; the runner
// only skips accounts still inside their auto-probe cooldown lock.
type AccountPoolProbeRunner struct {
	accountRepo    AccountRepository
	settingService *SettingService
	usageService   *AccountUsageService
	grokQuota      *GrokQuotaService
	cfg            *config.Config

	cron      *cron.Cron
	startOnce sync.Once
	stopOnce  sync.Once

	mu           sync.Mutex
	lastRunAt    time.Time
	inFlight     bool
	cooldownMemo sync.Map // accountID -> time.Time of last auto probe
}

func NewAccountPoolProbeRunner(
	accountRepo AccountRepository,
	settingService *SettingService,
	usageService *AccountUsageService,
	grokQuota *GrokQuotaService,
	cfg *config.Config,
) *AccountPoolProbeRunner {
	return &AccountPoolProbeRunner{
		accountRepo:    accountRepo,
		settingService: settingService,
		usageService:   usageService,
		grokQuota:      grokQuota,
		cfg:            cfg,
	}
}

func (s *AccountPoolProbeRunner) Start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		loc := time.Local
		if s.cfg != nil {
			if parsed, err := time.LoadLocation(s.cfg.Timezone); err == nil && parsed != nil {
				loc = parsed
			}
		}
		c := cron.New(cron.WithParser(scheduledTestCronParser), cron.WithLocation(loc))
		if _, err := c.AddFunc(accountPoolProbeDefaultTickCron, func() { s.tick() }); err != nil {
			logger.LegacyPrintf("service.account_pool_probe", "[AccountPoolProbe] not started: %v", err)
			return
		}
		s.cron = c
		s.cron.Start()
		logger.LegacyPrintf("service.account_pool_probe", "[AccountPoolProbe] started (tick=%s)", accountPoolProbeDefaultTickCron)
	})
}

func (s *AccountPoolProbeRunner) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.cron != nil {
			ctx := s.cron.Stop()
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
				logger.LegacyPrintf("service.account_pool_probe", "[AccountPoolProbe] cron stop timed out")
			}
		}
	})
}

func (s *AccountPoolProbeRunner) tick() {
	if s == nil || s.settingService == nil || s.accountRepo == nil {
		return
	}
	settings, err := s.settingService.GetAccountPoolProbeSettings(context.Background())
	if err != nil || settings == nil || !settings.Enabled {
		return
	}

	s.mu.Lock()
	if s.inFlight {
		s.mu.Unlock()
		return
	}
	interval := time.Duration(settings.IntervalMinutes) * time.Minute
	if interval < 10*time.Minute {
		interval = 10 * time.Minute
	}
	if !s.lastRunAt.IsZero() && time.Since(s.lastRunAt) < interval {
		s.mu.Unlock()
		return
	}
	s.inFlight = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.inFlight = false
		s.lastRunAt = time.Now()
		s.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), accountPoolProbeRunTimeout)
	defer cancel()
	s.runOnce(ctx, settings)
}

func (s *AccountPoolProbeRunner) runOnce(ctx context.Context, settings *AccountPoolProbeSettings) {
	candidates := s.collectCandidates(ctx, settings)
	if len(candidates) == 0 {
		return
	}

	maxConcurrency := settings.MaxConcurrency
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	for _, account := range candidates {
		if ctx.Err() != nil {
			break
		}
		account := account
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			s.probeOne(ctx, account, settings)
		}()
	}
	wg.Wait()
	slog.Info("account_pool_probe_round_done", "count", len(candidates), "platforms", settings.Platforms)
}

func (s *AccountPoolProbeRunner) collectCandidates(ctx context.Context, settings *AccountPoolProbeSettings) []*Account {
	batch := settings.BatchSize
	if batch < 1 {
		batch = 1
	}
	cooldown := time.Duration(settings.AccountCooldownMinutes) * time.Minute
	if cooldown < 10*time.Minute {
		cooldown = 10 * time.Minute
	}
	now := time.Now()
	out := make([]*Account, 0, batch)

	for _, platform := range settings.Platforms {
		if len(out) >= batch || ctx.Err() != nil {
			break
		}
		platform = strings.ToLower(strings.TrimSpace(platform))
		accounts, err := s.accountRepo.ListByPlatform(ctx, platform)
		if err != nil {
			slog.Warn("account_pool_probe_list_failed", "platform", platform, "error", err)
			continue
		}
		for i := range accounts {
			account := accounts[i]
			if len(out) >= batch {
				break
			}
			if !s.eligible(&account, now, cooldown) {
				continue
			}
			copied := account
			out = append(out, &copied)
		}
	}
	return out
}

func (s *AccountPoolProbeRunner) eligible(account *Account, now time.Time, cooldown time.Duration) bool {
	if account == nil || account.ID <= 0 {
		return false
	}
	if account.Status != StatusActive || !account.Schedulable {
		return false
	}
	// Prefer currently rate-limited / recently 429 accounts first is done by
	// natural ordering + cooldown; still allow healthy accounts for recovery.
	if raw, ok := s.cooldownMemo.Load(account.ID); ok {
		if last, ok := raw.(time.Time); ok && now.Sub(last) < cooldown {
			return false
		}
	}
	if account.Extra != nil {
		if raw, ok := account.Extra[accountPoolProbeExtraKey]; ok {
			if stamp, ok := raw.(string); ok {
				if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(stamp)); err == nil && now.Sub(parsed) < cooldown {
					return false
				}
			}
		}
	}
	switch account.Platform {
	case PlatformGrok:
		return account.IsGrokOAuth()
	case PlatformOpenAI:
		return account.IsOAuth()
	default:
		return false
	}
}

func (s *AccountPoolProbeRunner) probeOne(ctx context.Context, account *Account, settings *AccountPoolProbeSettings) {
	if account == nil {
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, accountPoolProbeSingleTimeout)
	defer cancel()

	var err error
	switch account.Platform {
	case PlatformGrok:
		if s.grokQuota == nil {
			return
		}
		// Active usage probe is the cheapest path that can both mark 429 and recover 200.
		_, err = s.grokQuota.ProbeUsage(probeCtx, account.ID)
	case PlatformOpenAI:
		if s.usageService == nil {
			return
		}
		_, err = s.usageService.GetUsage(probeCtx, account.ID, true)
	default:
		return
	}

	now := time.Now().UTC()
	s.cooldownMemo.Store(account.ID, now)
	if s.accountRepo != nil {
		_ = s.accountRepo.UpdateExtra(context.Background(), account.ID, map[string]any{
			accountPoolProbeExtraKey: now.Format(time.RFC3339),
		})
	}
	if err != nil {
		slog.Debug("account_pool_probe_account_failed", "account_id", account.ID, "platform", account.Platform, "error", err)
	}
}
