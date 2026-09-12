package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/robfig/cron/v3"
)

// CodeBuddy 养号任务：签到 / 活跃上报 / token 保活三类独立排程（仅 CN 区）。
//
// 移植自 workbuddy2api internal/scheduler，适配 sub2api 的账号仓库 + 设置体系：
//   - 账号来源：accountRepo.ListByPlatform(codebuddy)，非 active 的跳过；
//   - Global 区账号全部跳过（该区无签到活动，调用恒 code=10001，只是噪音）；
//   - 签到成功后查余额，余额 > 0 时清掉限流/临时不可调度冷却（对应
//     workbuddy2api 的 ReenableIfCredits）；
//   - 保活即 token 刷新；12153 session 死亡 → SetError 禁用账号（人工重登）；
//   - 账号间限速 AccountDelayMs（默认 800ms），逐号串行执行，防上游风控。
const (
	codebuddyMaintenanceTickCron  = "0 * * * *" // 每小时整点检查任务表
	codebuddyMaintenanceTimeout   = 10 * time.Minute
	codebuddyMaintenanceSingleTTL = 30 * time.Second
	codebuddyCheckinExtraKey      = "codebuddy_checkin_at"
	codebuddyActivityExtraKey     = "codebuddy_activity_at"
)

// CodebuddyMaintenanceRunner CodeBuddy 养号任务运行器。
type CodebuddyMaintenanceRunner struct {
	accountRepo    AccountRepository
	gatewayService *CodebuddyGatewayService
	refresher      *CodebuddyTokenRefresher
	settingService *SettingService
	cfg            *config.Config

	cron      *cron.Cron
	startOnce sync.Once
	stopOnce  sync.Once

	mu       sync.Mutex
	inFlight map[string]bool // task name -> running
}

func NewCodebuddyMaintenanceRunner(
	accountRepo AccountRepository,
	gatewayService *CodebuddyGatewayService,
	refresher *CodebuddyTokenRefresher,
	settingService *SettingService,
	cfg *config.Config,
) *CodebuddyMaintenanceRunner {
	return &CodebuddyMaintenanceRunner{
		accountRepo:    accountRepo,
		gatewayService: gatewayService,
		refresher:      refresher,
		settingService: settingService,
		cfg:            cfg,
		inFlight:       make(map[string]bool),
	}
}

func (s *CodebuddyMaintenanceRunner) Start() {
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
		if _, err := c.AddFunc(codebuddyMaintenanceTickCron, func() { s.tick() }); err != nil {
			slog.Error("codebuddy_maintenance.not_started", "error", err)
			return
		}
		s.cron = c
		s.cron.Start()
		slog.Info("codebuddy_maintenance.started", "tick", codebuddyMaintenanceTickCron)
	})
}

func (s *CodebuddyMaintenanceRunner) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.cron != nil {
			ctx := s.cron.Stop()
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
				slog.Warn("codebuddy_maintenance.cron_stop_timed_out")
			}
		}
	})
}

// tick 每小时整点检查：当前小时命中哪个任务表就执行哪个任务。
func (s *CodebuddyMaintenanceRunner) tick() {
	if s == nil || s.settingService == nil || s.accountRepo == nil {
		return
	}
	settings, err := s.settingService.GetCodebuddyMaintenanceSettings(context.Background())
	if err != nil || settings == nil || !settings.Enabled {
		return
	}

	now := time.Now()
	hour := now.Hour()
	if settings.CheckinEnabled && codebuddyHourMatches(settings.CheckinHours, hour) {
		s.runExclusive("checkin", settings, s.runCheckin)
	}
	if settings.ActivityEnabled && codebuddyHourMatches(settings.ActivityHours, hour) {
		s.runExclusive("activity", settings, s.runActivity)
	}
	if settings.KeepaliveEnabled && codebuddyHourMatches(settings.KeepaliveHours, hour) {
		s.runExclusive("keepalive", settings, s.runKeepalive)
	}
}

// runExclusive 同类任务不并发（上一轮没跑完就跳过本轮）。
func (s *CodebuddyMaintenanceRunner) runExclusive(task string, settings *CodebuddyMaintenanceSettings, fn func(context.Context, *CodebuddyMaintenanceSettings)) {
	s.mu.Lock()
	if s.inFlight[task] {
		s.mu.Unlock()
		return
	}
	s.inFlight[task] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.inFlight, task)
		s.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), codebuddyMaintenanceTimeout)
	defer cancel()
	fn(ctx, settings)
}

// listCNAccounts 列出全部 active 的 CN 区 CodeBuddy 账号（Global 跳过）。
func (s *CodebuddyMaintenanceRunner) listCNAccounts(ctx context.Context) []*Account {
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformCodebuddy)
	if err != nil {
		slog.Warn("codebuddy_maintenance.list_failed", "error", err)
		return nil
	}
	out := make([]*Account, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if account.Status != StatusActive {
			continue
		}
		if codebuddy.RegionForAccountType(account.Type) != codebuddy.RegionCN {
			continue
		}
		out = append(out, account)
	}
	return out
}

func (s *CodebuddyMaintenanceRunner) accountDelay(settings *CodebuddyMaintenanceSettings) time.Duration {
	if settings.AccountDelayMs > 0 {
		return time.Duration(settings.AccountDelayMs) * time.Millisecond
	}
	return 0
}

// runCheckin 签到 + 余额刷新 + 解冻。
// 临时不可调度的账号也参与（签到就是为了解冻它们）；SetError 的不参与（人工处理）。
func (s *CodebuddyMaintenanceRunner) runCheckin(ctx context.Context, settings *CodebuddyMaintenanceSettings) {
	accounts := s.listCNAccounts(ctx)
	if len(accounts) == 0 {
		return
	}
	checkinCount, unfrozeCount := 0, 0
	for i, account := range accounts {
		if ctx.Err() != nil {
			break
		}
		if i > 0 {
			select {
			case <-time.After(s.accountDelay(settings)):
			case <-ctx.Done():
				return
			}
		}
		// 签到；「已签到」等业务错误也继续走余额查询（可能今天已签但昨天冻结待解）
		if err := s.dailyCheckin(ctx, account); err != nil {
			slog.Debug("codebuddy_checkin.failed", "account_id", account.ID, "error", err)
		} else {
			checkinCount++
		}
		// 余额刷新 + 解冻
		if s.gatewayService == nil {
			continue
		}
		remain, err := s.gatewayService.FetchCreditRemain(ctx, account)
		if err != nil {
			slog.Debug("codebuddy_checkin.credit_failed", "account_id", account.ID, "error", err)
			continue
		}
		if remain > 0 {
			if unfroze := s.unfreezeAccount(ctx, account); unfroze {
				unfrozeCount++
			}
		}
	}
	slog.Info("codebuddy_maintenance.checkin_done",
		"accounts", len(accounts), "checked_in", checkinCount, "unfroze", unfrozeCount)
}

// runActivity 活跃上报：一条 chat_request_send 事件同时点亮 growth 连登。
func (s *CodebuddyMaintenanceRunner) runActivity(ctx context.Context, settings *CodebuddyMaintenanceSettings) {
	accounts := s.listCNAccounts(ctx)
	if len(accounts) == 0 {
		return
	}
	reported := 0
	for i, account := range accounts {
		if ctx.Err() != nil {
			break
		}
		if i > 0 {
			select {
			case <-time.After(s.accountDelay(settings)):
			case <-ctx.Done():
				return
			}
		}
		// 无 access token 的跳过（活动上报不带 refresh token）
		creds := codebuddy.FromCredentialsMap(account.Credentials)
		if creds.AccessToken == "" {
			continue
		}
		if err := s.reportChatActivity(ctx, account, creds); err != nil {
			slog.Debug("codebuddy_activity.failed", "account_id", account.ID, "error", err)
			continue
		}
		reported++
	}
	slog.Info("codebuddy_maintenance.activity_done", "accounts", len(accounts), "reported", reported)
}

// runKeepalive 保活 = token 刷新；12153 session 死亡自动禁用账号。
func (s *CodebuddyMaintenanceRunner) runKeepalive(ctx context.Context, settings *CodebuddyMaintenanceSettings) {
	accounts := s.listCNAccounts(ctx)
	if len(accounts) == 0 {
		return
	}
	refreshed, disabled := 0, 0
	for i, account := range accounts {
		if ctx.Err() != nil {
			break
		}
		if i > 0 {
			select {
			case <-time.After(s.accountDelay(settings)):
			case <-ctx.Done():
				return
			}
		}
		if s.refresher == nil {
			continue
		}
		newCreds, err := s.refresher.Refresh(ctx, account)
		if err != nil {
			var ue *codebuddy.Error
			if errors.As(err, &ue) && ue.Kind == codebuddy.ErrSessionDead {
				// session 死亡：人工重登才能恢复，直接禁用
				if setErr := s.accountRepo.SetError(ctx, account.ID, "CodeBuddy session dead (12153): re-login required"); setErr != nil {
					slog.Warn("codebuddy_keepalive.set_error_failed", "account_id", account.ID, "error", setErr)
				} else {
					disabled++
					slog.Info("codebuddy_keepalive.session_dead_disabled", "account_id", account.ID)
				}
				continue
			}
			slog.Debug("codebuddy_keepalive.failed", "account_id", account.ID, "error", err)
			continue
		}
		if err := s.persistRefreshedCredentials(ctx, account, newCreds); err != nil {
			slog.Warn("codebuddy_keepalive.save_failed", "account_id", account.ID, "error", err)
			continue
		}
		refreshed++
	}
	slog.Info("codebuddy_maintenance.keepalive_done",
		"accounts", len(accounts), "refreshed", refreshed, "disabled", disabled)
}

// dailyCheckin 执行每日签到（POST /v2/billing/meter/daily-checkin，body {}）。
func (s *CodebuddyMaintenanceRunner) dailyCheckin(ctx context.Context, account *Account) error {
	creds := codebuddy.FromCredentialsMap(account.Credentials)
	if err := creds.Validate(); err != nil {
		return err
	}
	req, err := codebuddy.BuildDailyCheckinRequest(creds, codebuddy.RegionCN, codebuddy.EndpointOptions{})
	if err != nil {
		return err
	}
	resp, closeBody, err := s.doMaintenanceRequest(ctx, account, req)
	if err != nil {
		return err
	}
	defer closeBody()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("checkin HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	_, err = codebuddy.ParseEnvelope(resp.StatusCode, body)
	return err
}

// reportChatActivity 发送一条 chat_request_send 活跃上报事件（数组形，必含 userId）。
// 事件形状照抄 workbuddy2api upstream/report.go（全字段，勿精简，防上游加严）。
func (s *CodebuddyMaintenanceRunner) reportChatActivity(ctx context.Context, account *Account, creds codebuddy.Credentials) error {
	now := time.Now().UnixMilli()
	conversationID := fmt.Sprintf("sub2api-%d", now)
	event := map[string]any{
		"eventCode":             "chat_request_send",
		"timestamp":             now,
		"reportDelay":           0,
		"mode":                  "craft",
		"conversationId":        conversationID,
		"requestId":             conversationID,
		"inputLength":           12,
		"requestModelId":        "deepseek-v4-flash",
		"requestModelName":      "DeepSeek V4 Flash",
		"isPlan":                false,
		"isAutoExecuteTerminal": false,
		"isAutoModify":          false,
		"codebaseEnable":        false,
		"maxToken":              0,
		"maxSteps":              0,
		"temperature":           0,
		"maxRetries":            0,
		"mentionContexts":       []any{},
		"knowledgeId":           []any{},
		"knowledgeName":         []any{},
		"codebaseId":            "",
		"mentionContextCount":   0,
		"command":               "",
		"expertId":              "",
		"recommendId":           "",
		"skillId":               "",
		"skillCount":            0,
		"totalCount":            0,
		"fileUri":               "",
		"presentAt":             now,
		"traceId":               "",
		"rootRequestId":         conversationID,
		"parentConversationId":  conversationID,
		"agentName":             "default",
		"agentType":             "conversation",
		"userId":                creds.UID,
	}
	raw, err := json.Marshal([]any{event})
	if err != nil {
		return err
	}
	req, err := codebuddy.BuildReportRequest(creds, codebuddy.RegionCN, raw, codebuddy.EndpointOptions{})
	if err != nil {
		return err
	}
	resp, closeBody, err := s.doMaintenanceRequest(ctx, account, req)
	if err != nil {
		return err
	}
	defer closeBody()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	_, err = codebuddy.ParseEnvelope(resp.StatusCode, body)
	return err
}

// doMaintenanceRequest 经 HTTPUpstream 执行养号请求（带代理 / TLS 指纹 / 并发槽）。
func (s *CodebuddyMaintenanceRunner) doMaintenanceRequest(ctx context.Context, account *Account, req *http.Request) (*http.Response, func(), error) {
	if s.gatewayService == nil || s.gatewayService.httpUpstream == nil {
		return nil, nil, errors.New("codebuddy maintenance: http upstream is not configured")
	}
	req = req.WithContext(ctx)
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	var tlsProfile = (*tlsfingerprint.Profile)(nil)
	if s.gatewayService.tlsFPProfileService != nil {
		tlsProfile = s.gatewayService.tlsFPProfileService.ResolveTLSProfile(account)
	}
	resp, err := s.gatewayService.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	if err != nil {
		return nil, nil, err
	}
	closeBody := func() { _ = resp.Body.Close() }
	return resp, closeBody, nil
}

// unfreezeAccount 余额恢复时清除限流/临时不可调度冷却（对应 ReenableIfCredits）。
func (s *CodebuddyMaintenanceRunner) unfreezeAccount(ctx context.Context, account *Account) bool {
	unfroze := false
	if account.IsRateLimited() || account.RateLimitResetAt != nil {
		if err := s.accountRepo.ClearRateLimit(ctx, account.ID); err == nil {
			unfroze = true
		}
	}
	if account.TempUnschedulableUntil != nil && time.Now().Before(*account.TempUnschedulableUntil) {
		if err := s.accountRepo.ClearTempUnschedulable(ctx, account.ID); err == nil {
			unfroze = true
		}
	}
	return unfroze
}

// persistRefreshedCredentials 把刷新后的凭证写回账号。
func (s *CodebuddyMaintenanceRunner) persistRefreshedCredentials(ctx context.Context, account *Account, newCreds map[string]any) error {
	if len(newCreds) == 0 {
		return nil
	}
	for k, v := range newCreds {
		if strings.TrimSpace(k) == "" {
			continue
		}
		if account.Credentials == nil {
			account.Credentials = make(map[string]any)
		}
		account.Credentials[k] = v
	}
	return s.accountRepo.Update(ctx, account)
}

// codebuddyHourMatches 报告 hour 是否命中小时表。
func codebuddyHourMatches(hours []int, hour int) bool {
	for _, h := range hours {
		if h == hour {
			return true
		}
	}
	return false
}
