package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// 调度策略反哺（阶段 B）——来源 workbuddy2api internal/pool/pool.go 的
// 双计数器退避 + 三因子加权选号。落地为 sub2api 风格：
//   - 全部退避状态挂在 Account.Extra（无 DB 迁移），软退避/熔断计数器持久化在
//     extra["scheduler_backoff"]；success/err 累计统计为进程内内存态
//     （与 workbuddy2api 原始实现一致；重启清零 → 新号回中性权重 1.5）
//   - 由 setting（scheduler_weighted_selection_settings）门控；关闭时
//     调度路径与改动前逐字一致（零行为变化），可整体回滚
//
// 与 workbuddy2api 的有意差异：sub2api 已有 RateLimitResetAt（即时冷却）与
// TempUnschedulableUntil（请求级封禁）两条机制，本模块不取代它们——
//   - 软退避（softStreak）：仅在「现有机制给出的冷却时长」上做连续限流的
//     指数放大（600s 基数，d<<(streak-1)，封顶 2h），由 TempUnscheduleRetryableError
//     的 CodeBuddy 分支（ErrSoftRate/ErrNotFound/ErrServer 类 429/5xx 错误在
//     同账号重试用尽后触发）调用；冷却写既有 TempUnschedulableUntil
//     （快照投影必带该列，Layer2 选号 IsSchedulable 自动过滤），
//     计数器持久化 extra["scheduler_backoff"]（重启后 streak 不丢）
//   - 熔断（breaker）：连续失败计数达到阈值后 30m×2^n 封顶 6h 的长冷却，
//     独立于软退避（两条升级线互不污染，与原实现一致）；触发时同样并入
//     TempUnschedulableUntil 的扩展时刻，因此候选过滤无需读 extra
//   - 成功清零双触发点：转发成功（RecordUsage）与签到解冻（maintenance
//     unfreeze）都调 ClearSchedulerBackoff
//   - 三因子加权选号：可调度候选集上按
//     credits占比×10 + idleWeight(min(闲置小时×0.5,5)) + successRate×3
//     降序取 Top-5，再在 Top-5 内定点加权随机（防惊群：进程内 lastPick 记录）

// schedulerBackoffExtraKey 存放退避状态（soft_streak / breaker_*）。
// 非 neutral 键：写入自动触发调度快照失效（account_repo.go 的 outbox 机制），
// 让冷却状态进入候选投影。
const schedulerBackoffExtraKey = "scheduler_backoff"

// SchedulerWeightedSelectionSettings 三因子加权选号配置。
type SchedulerWeightedSelectionSettings struct {
	// Enabled 总开关。false 时调度路径与现有行为逐字一致。
	Enabled bool `json:"enabled"`
	// TopN 短名单大小（默认 5）。
	TopN int `json:"top_n"`
	// IdleWeightPerHour 闲置补偿系数（默认 0.5）。
	IdleWeightPerHour float64 `json:"idle_weight_per_hour"`
	// IdleWeightMax 闲置补偿封顶（默认 5.0）。
	IdleWeightMax float64 `json:"idle_weight_max"`
	// MinPickGapMs 防惊群：跳过最近 N 毫秒内刚选中的账号（默认 100ms，0 关闭）。
	MinPickGapMs int `json:"min_pick_gap_ms"`
}

// DefaultSchedulerWeightedSelectionSettings 与 workbuddy2api 默认值对齐。
func DefaultSchedulerWeightedSelectionSettings() *SchedulerWeightedSelectionSettings {
	return &SchedulerWeightedSelectionSettings{
		Enabled:           false, // 默认关闭：现有调度行为不变
		TopN:              5,
		IdleWeightPerHour: 0.5,
		IdleWeightMax:     5.0,
		MinPickGapMs:      100,
	}
}

// GetSchedulerWeightedSelectionSettings 读取加权选号配置（缺省给默认值）。
func (s *SettingService) GetSchedulerWeightedSelectionSettings(ctx context.Context) (*SchedulerWeightedSelectionSettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeySchedulerWeightedSelection)
	if err != nil || value == "" {
		return DefaultSchedulerWeightedSelectionSettings(), nil
	}
	var settings SchedulerWeightedSelectionSettings
	if json.Unmarshal([]byte(value), &settings) != nil {
		return DefaultSchedulerWeightedSelectionSettings(), nil
	}
	return normalizeSchedulerWeightedSelectionSettings(&settings), nil
}

// SetSchedulerWeightedSelectionSettings 写入加权选号配置。
func (s *SettingService) SetSchedulerWeightedSelectionSettings(ctx context.Context, settings *SchedulerWeightedSelectionSettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	normalized := normalizeSchedulerWeightedSelectionSettings(settings)
	data, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal scheduler weighted selection settings: %w", err)
	}
	return s.settingRepo.Set(ctx, SettingKeySchedulerWeightedSelection, string(data))
}

func normalizeSchedulerWeightedSelectionSettings(settings *SchedulerWeightedSelectionSettings) *SchedulerWeightedSelectionSettings {
	defaults := DefaultSchedulerWeightedSelectionSettings()
	out := *settings
	if out.TopN <= 0 {
		out.TopN = defaults.TopN
	}
	if out.IdleWeightPerHour <= 0 {
		out.IdleWeightPerHour = defaults.IdleWeightPerHour
	}
	if out.IdleWeightMax <= 0 {
		out.IdleWeightMax = defaults.IdleWeightMax
	}
	if out.MinPickGapMs < 0 {
		out.MinPickGapMs = defaults.MinPickGapMs
	}
	return &out
}

// ── 双计数器退避 ───────────────────────────────────────────────────────────

const (
	// softRateBase 软冷却退避基数（600s，与 workbuddy2api 一致）。
	softRateBase = 600 * time.Second
	// softRateMax 软冷却退避封顶（2h）。
	softRateMax = 2 * time.Hour
	// softStreakShiftMax 左移位数上限（防溢出）。
	softStreakShiftMax = 16
	// breakerThreshold 连续失败触发熔断的阈值。
	breakerThreshold = 3
	// breakerCooldownBase 熔断基数（30m）。
	breakerCooldownBase = 30 * time.Minute
	// breakerCooldownMax 熔断封顶（6h）。
	breakerCooldownMax = 6 * time.Hour
)

// schedulerBackoffState 退避状态（挂 extra["scheduler_backoff"]）。
type schedulerBackoffState struct {
	SoftStreak         int   `json:"soft_streak,omitempty"`
	BreakerRetryCount  int   `json:"breaker_retry_count,omitempty"`
	BreakerConsecutive int   `json:"breaker_consecutive,omitempty"`
	BreakerUntil       int64 `json:"breaker_until,omitempty"` // unix 秒；0 = 不在熔断期
}

func schedulerBackoffFromAccount(account *Account) schedulerBackoffState {
	if account == nil || account.Extra == nil {
		return schedulerBackoffState{}
	}
	raw, ok := account.Extra[schedulerBackoffExtraKey]
	if !ok || raw == nil {
		return schedulerBackoffState{}
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return schedulerBackoffState{}
	}
	var state schedulerBackoffState
	if json.Unmarshal(body, &state) != nil {
		return schedulerBackoffState{}
	}
	return state
}

// softCooldownDuration 计算第 streak 次连续软限流的冷却时长：
// base << (streak-1)，封顶 softRateMax；streak<=1 返回基数（与单次行为一致）。
func softCooldownDuration(streak int) time.Duration {
	if streak <= 1 {
		return softRateBase
	}
	shift := streak - 1
	if shift > softStreakShiftMax {
		shift = softStreakShiftMax
	}
	d := softRateBase << shift
	if d <= 0 || d > softRateMax {
		return softRateMax
	}
	return d
}

// breakerCooldownDuration 计算第 retryCount 次熔断的冷却时长：30m × 2^n 封顶 6h。
func breakerCooldownDuration(retryCount int) time.Duration {
	d := breakerCooldownBase
	for i := 1; i < retryCount; i++ {
		d *= 2
		if d >= breakerCooldownMax {
			return breakerCooldownMax
		}
	}
	return d
}

// RecordSoftRateLimit 记录一次软限流（429/可重试上游错误）：递增 softStreak 并
// 返回放大后的冷却时长。同时喂入熔断计数器（两条升级线计数器独立，触发熔断时
// 第二个返回值给出熔断截止时刻，调用方取 max 后写入 TempUnschedulableUntil）。
func RecordSoftRateLimit(account *Account) (time.Duration, *time.Time) {
	state := schedulerBackoffFromAccount(account)
	state.SoftStreak++
	cooldown := softCooldownDuration(state.SoftStreak)
	prevRetryCount := state.BreakerRetryCount
	state = recordBreakerFailure(state)
	writeSchedulerBackoffState(account, state)
	var breakerExtended *time.Time
	if state.BreakerRetryCount > prevRetryCount && state.BreakerUntil > 0 {
		t := time.Unix(state.BreakerUntil, 0)
		breakerExtended = &t
	}
	return cooldown, breakerExtended
}

// RecordBreakerFailure 记录一次普通失败（不触发软退避，只喂熔断计数器）。
// 触发熔断时返回熔断截止时刻（调用方据此扩展 TempUnschedulableUntil），否则 nil。
func RecordBreakerFailure(account *Account) *time.Time {
	if account == nil {
		return nil
	}
	state := schedulerBackoffFromAccount(account)
	prevRetryCount := state.BreakerRetryCount
	state = recordBreakerFailure(state)
	writeSchedulerBackoffState(account, state)
	if state.BreakerRetryCount > prevRetryCount && state.BreakerUntil > 0 {
		t := time.Unix(state.BreakerUntil, 0)
		return &t
	}
	return nil
}

func recordBreakerFailure(state schedulerBackoffState) schedulerBackoffState {
	state.BreakerConsecutive++
	if state.BreakerConsecutive >= breakerThreshold {
		state.BreakerRetryCount++
		state.BreakerUntil = time.Now().Add(breakerCooldownDuration(state.BreakerRetryCount)).Unix()
		state.BreakerConsecutive = 0
	}
	return state
}

// writeSchedulerBackoffState 把退避状态写回账号内存 Extra（全零则删键）。
// 持久化由调用方经 UpdateExtra 完成（scheduler_backoff 非 neutral 键，
// 写入自动触发调度快照失效，冷却状态进入候选投影）。
func writeSchedulerBackoffState(account *Account, state schedulerBackoffState) {
	if account == nil {
		return
	}
	if state == (schedulerBackoffState{}) {
		delete(account.Extra, schedulerBackoffExtraKey)
		return
	}
	if account.Extra == nil {
		account.Extra = make(map[string]any)
	}
	account.Extra[schedulerBackoffExtraKey] = state
}

// RecordSchedulerSuccess 账号请求成功：清零账号内存 Extra 中的全部退避状态
// （成功即证明恢复）。持久化删除由调用方（RecordUsage / 签到解冻）完成。
func RecordSchedulerSuccess(account *Account) {
	if account == nil {
		return
	}
	writeSchedulerBackoffState(account, schedulerBackoffState{})
}

// SchedulerBreakerStatePresent 报告账号 extra 是否携带非空退避状态（决定成功清零
// /解冻清零是否需要写库）。
func SchedulerBackoffPresent(account *Account) bool {
	if account == nil || account.Extra == nil {
		return false
	}
	raw, ok := account.Extra[schedulerBackoffExtraKey]
	return ok && raw != nil
}

// ── 三因子加权选号 ─────────────────────────────────────────────────────────

// schedulerStats 累计统计（进程内内存态，重启清零 → 回中性权重）。
type schedulerStats struct {
	SuccessCount int64 `json:"success_count,omitempty"`
	ErrTotal     int64 `json:"err_total,omitempty"`
}

// schedulerWeightOf 计算账号三因子权重（workbuddy2api weightOf 原样）：
// credits比例×10 + 闲置补偿（从未使用给满分）+ 成功率×3（无记录给中性 1.5）。
func schedulerWeightOf(account *Account, maxCredits int64, stats schedulerStats, settings *SchedulerWeightedSelectionSettings, now time.Time) float64 {
	w := 1.0
	if maxCredits > 0 {
		if credits := accountCodebuddyCredits(account); credits > 0 {
			w += float64(credits) / float64(maxCredits) * 10
		}
	}
	if account.LastUsedAt == nil {
		w += settings.IdleWeightMax
	} else {
		hours := now.Sub(*account.LastUsedAt).Hours()
		if hours < 0 {
			hours = 0
		}
		idleW := hours * settings.IdleWeightPerHour
		if idleW > settings.IdleWeightMax {
			idleW = settings.IdleWeightMax
		}
		w += idleW
	}
	totalReq := stats.SuccessCount + stats.ErrTotal
	if totalReq > 0 {
		w += float64(stats.SuccessCount) / float64(totalReq) * 3
	} else {
		w += 1.5
	}
	return w
}

// accountCodebuddyCredits 读 CodeBuddy 账号的积分余额快照（extra.codebuddy_credit.remain）。
func accountCodebuddyCredits(account *Account) int64 {
	if account == nil || account.Extra == nil {
		return 0
	}
	raw, ok := account.Extra["codebuddy_credit"]
	if !ok || raw == nil {
		return 0
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return 0
	}
	var credit struct {
		Remain int64 `json:"remain"`
	}
	if json.Unmarshal(body, &credit) != nil {
		return 0
	}
	return credit.Remain
}

// SelectBySchedulerWeight 在候选集中做三因子加权选号：
// 权重降序取 Top-N，再在 Top-N 内按同一权重定点加权随机。
// 候选集为空返回 nil。lastPick / statsOf 由 schedulerWeightedRuntime 注入
// （进程内 lastPick 记录防惊群、stats 记录成功率；nil 时按无记录处理）。
// 防惊群：MinPickGapMs>0 时排除 MinPickGap 内刚被选中的账号；Top-N 全部
// 命中时保留原 Top-N，退化为加权随机。
func SelectBySchedulerWeight(accounts []*Account, settings *SchedulerWeightedSelectionSettings, lastPick func(accountID int64) (time.Time, bool), statsOf func(accountID int64) schedulerStats) *Account {
	if len(accounts) == 0 {
		return nil
	}
	if len(accounts) == 1 {
		return accounts[0]
	}
	now := time.Now()

	var maxCredits int64
	for _, account := range accounts {
		if credits := accountCodebuddyCredits(account); credits > maxCredits {
			maxCredits = credits
		}
	}

	type weighted struct {
		account *Account
		weight  float64
	}
	ws := make([]weighted, 0, len(accounts))
	for _, account := range accounts {
		var stats schedulerStats
		if statsOf != nil {
			stats = statsOf(account.ID)
		}
		ws = append(ws, weighted{account: account, weight: schedulerWeightOf(account, maxCredits, stats, settings, now)})
	}
	sort.SliceStable(ws, func(i, j int) bool { return ws[i].weight > ws[j].weight })

	topN := settings.TopN
	if topN > len(ws) {
		topN = len(ws)
	}
	ws = ws[:topN]

	// 防惊群过滤：Top-N 内排除 MinPickGap 内刚被选中的（除非全排除后为空）。
	if settings.MinPickGapMs > 0 && lastPick != nil {
		gap := time.Duration(settings.MinPickGapMs) * time.Millisecond
		filtered := make([]weighted, 0, len(ws))
		for _, w := range ws {
			if last, ok := lastPick(w.account.ID); ok && now.Sub(last) < gap {
				continue
			}
			filtered = append(filtered, w)
		}
		if len(filtered) > 0 {
			ws = filtered
		}
	}

	// Top-N 内按权重定点加权随机（权重只决定中签概率，不再排序截断）。
	total := 0.0
	for _, w := range ws {
		total += w.weight
	}
	if total <= 0 || math.IsNaN(total) {
		return ws[rand.Intn(len(ws))].account
	}
	pick := rand.Float64() * total
	for _, w := range ws {
		pick -= w.weight
		if pick <= 0 {
			return w.account
		}
	}
	return ws[len(ws)-1].account
}

// ── 进程内运行时（setting TTL 缓存 + 防惊群 lastPick + 成功/失败统计）────────

// schedulerWeightedSettingsTTL setting 缓存有效期：关闭态热路径每 60s 最多
// 一次 DB 读；开启态选号精度对 60s 陈旧度不敏感。
const schedulerWeightedSettingsTTL = 60 * time.Second

type schedulerWeightedSettingsSnapshot struct {
	settings  *SchedulerWeightedSelectionSettings
	expiresAt time.Time
}

// schedulerWeightedRuntime 加权选号的进程内状态。所有字段在关闭态零访问
// （enabled() 短路后调用方不会触碰其余方法）。
type schedulerWeightedRuntime struct {
	settingService *SettingService

	cached      atomic.Pointer[schedulerWeightedSettingsSnapshot]
	lastPick    sync.Map // accountID int64 -> time.Time
	stats       sync.Map // accountID int64 -> schedulerStats
	backoffMark sync.Map // accountID int64 -> struct{}：退避计数器在库里有脏状态，下次成功需清
}

func newSchedulerWeightedRuntime(settingService *SettingService) *schedulerWeightedRuntime {
	return &schedulerWeightedRuntime{settingService: settingService}
}

// settings 读取加权选号配置（TTL 缓存；settingService 缺失时返回默认关闭值）。
func (r *schedulerWeightedRuntime) settings(ctx context.Context) *SchedulerWeightedSelectionSettings {
	if r == nil || r.settingService == nil {
		return DefaultSchedulerWeightedSelectionSettings()
	}
	if snap := r.cached.Load(); snap != nil && time.Now().Before(snap.expiresAt) {
		return snap.settings
	}
	settings, err := r.settingService.GetSchedulerWeightedSelectionSettings(ctx)
	if err != nil {
		settings = DefaultSchedulerWeightedSelectionSettings()
	}
	r.cached.Store(&schedulerWeightedSettingsSnapshot{
		settings:  settings,
		expiresAt: time.Now().Add(schedulerWeightedSettingsTTL),
	})
	return settings
}

func (r *schedulerWeightedRuntime) enabled(ctx context.Context) bool {
	return r != nil && r.settings(ctx).Enabled
}

// lastPickFunc 返回防惊群查询回调（Top-N 内排除 gap 内刚选中的账号）。
func (r *schedulerWeightedRuntime) lastPickFunc() func(accountID int64) (time.Time, bool) {
	if r == nil {
		return nil
	}
	return func(accountID int64) (time.Time, bool) {
		v, ok := r.lastPick.Load(accountID)
		if !ok {
			return time.Time{}, false
		}
		return v.(time.Time), true
	}
}

// notePick 记录一次成功选中（确认拿到槽位/会话后调用）。
func (r *schedulerWeightedRuntime) notePick(accountID int64) {
	if r != nil {
		r.lastPick.Store(accountID, time.Now())
	}
}

func (r *schedulerWeightedRuntime) statsOf(accountID int64) schedulerStats {
	if r == nil {
		return schedulerStats{}
	}
	v, ok := r.stats.Load(accountID)
	if !ok {
		return schedulerStats{}
	}
	return v.(schedulerStats)
}

// recordRequestOutcome 累计一次请求结果（成功 / 失败各一）。
func (r *schedulerWeightedRuntime) recordRequestOutcome(accountID int64, success bool) {
	if r == nil {
		return
	}
	v, _ := r.stats.LoadOrStore(accountID, schedulerStats{})
	stats := v.(schedulerStats)
	if success {
		stats.SuccessCount++
	} else {
		stats.ErrTotal++
	}
	r.stats.Store(accountID, stats)
}

// resetStats 清零账号统计（解冻时调用；退避计数器持久化在 extra，由
// 调用方经 UpdateExtra 删除）。
func (r *schedulerWeightedRuntime) resetStats(accountID int64) {
	if r != nil {
		r.stats.Delete(accountID)
	}
}

// ── GatewayService 接线辅助 ────────────────────────────────────────────────

// schedulerWeightedSelectionEnabled 报告加权选号是否开启（setting 门控）。
// 关闭时调用方逐字走现有「优先级 → 最早重置 → 负载率 → LRU」路径。
func (s *GatewayService) schedulerWeightedSelectionEnabled(ctx context.Context) bool {
	return s != nil && s.schedulerWeighted != nil && s.schedulerWeighted.enabled(ctx)
}

// schedulerWeightedSelect 对候选集执行加权选号（setting / 防惊群 / stats 均由
// runtime 提供）。
func (s *GatewayService) schedulerWeightedSelect(ctx context.Context, accounts []*Account) *Account {
	if s == nil || s.schedulerWeighted == nil {
		return nil
	}
	return SelectBySchedulerWeight(accounts, s.schedulerWeighted.settings(ctx),
		s.schedulerWeighted.lastPickFunc(), s.schedulerWeighted.statsOf)
}

// schedulerWeightedNotePick 在加权选号成功拿到槽位后记录（防惊群）。
func (s *GatewayService) schedulerWeightedNotePick(accountID int64) {
	if s != nil && s.schedulerWeighted != nil {
		s.schedulerWeighted.notePick(accountID)
	}
}

// schedulerWeightedRecordOutcome 记录一次 CodeBuddy 请求结果（成功/失败统计，
// 进程内内存态；仅开启时累计，避免关闭态白占内存）。
func (s *GatewayService) schedulerWeightedRecordOutcome(ctx context.Context, accountID int64, success bool) {
	if !s.schedulerWeightedSelectionEnabled(ctx) {
		return
	}
	s.schedulerWeighted.recordRequestOutcome(accountID, success)
}

// recordCodebuddySchedulerSuccess CodeBuddy 请求成功（RecordUsage 路径）：累计
// 成功统计；若此前软失败持久化过退避计数器（backoffMark 脏标记），写显式 null
// 删除 extra 键（JSONB 合并；读取侧把 null 视为无状态）。关闭态零操作。
func (s *GatewayService) recordCodebuddySchedulerSuccess(ctx context.Context, account *Account) {
	if s == nil || s.schedulerWeighted == nil || account == nil || !account.IsCodebuddy() {
		return
	}
	r := s.schedulerWeighted
	if !r.enabled(ctx) {
		return
	}
	r.recordRequestOutcome(account.ID, true)
	if _, dirty := r.backoffMark.LoadAndDelete(account.ID); dirty {
		RecordSchedulerSuccess(account)
		_ = s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{schedulerBackoffExtraKey: nil})
	}
}
