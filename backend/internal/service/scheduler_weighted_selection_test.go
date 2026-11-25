//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ── 双计数器退避 ──────────────────────────────────────────────────────────

func TestSoftCooldownDuration_ExponentialWithCap(t *testing.T) {
	cases := []struct {
		streak int
		want   time.Duration
	}{
		{0, softRateBase}, // streak<=1 返回基数
		{1, softRateBase}, // 600s
		{2, 2 * softRateBase},
		{3, 4 * softRateBase}, // 2400s
		{100, softRateMax},    // 封顶 2h（shift 溢出保护）
	}
	for _, tc := range cases {
		require.Equalf(t, tc.want, softCooldownDuration(tc.streak), "streak=%d", tc.streak)
	}
}

func TestBreakerCooldownDuration_ExponentialWithCap(t *testing.T) {
	cases := []struct {
		n    int
		want time.Duration
	}{
		{1, 30 * time.Minute},
		{2, 60 * time.Minute},
		{3, 120 * time.Minute},
		{4, 240 * time.Minute},
		{10, breakerCooldownMax}, // 封顶 6h
	}
	for _, tc := range cases {
		require.Equalf(t, tc.want, breakerCooldownDuration(tc.n), "retryCount=%d", tc.n)
	}
}

func codebuddyBackoffAccount() *Account {
	return &Account{ID: 1, Platform: PlatformCodebuddy, Extra: map[string]any{}}
}

func TestRecordBreakerFailure_ThresholdTripsOnce(t *testing.T) {
	account := codebuddyBackoffAccount()
	// 前两次不触发熔断，返回 nil。
	require.Nil(t, RecordBreakerFailure(account))
	require.Nil(t, RecordBreakerFailure(account))
	// 第三次达到阈值触发，返回非空熔断截止时刻。
	until := RecordBreakerFailure(account)
	require.NotNil(t, until)
	require.True(t, until.After(time.Now()))
	// consecutive 已归零（第三次触发后）；再失败一次不应立即再次熔断。
	require.Nil(t, RecordBreakerFailure(account))
}

func TestRecordSoftRateLimit_FeedsBreakerAndReturnsCooldown(t *testing.T) {
	account := codebuddyBackoffAccount()
	// 第一次：streak=1 → 600s 冷却；熔断计数 1，未触发。
	cooldown, breakerExtended := RecordSoftRateLimit(account)
	require.Equal(t, softRateBase, cooldown)
	require.Nil(t, breakerExtended)

	// 第二次：streak=2 → 1200s；熔断计数 2，未触发。
	cooldown, breakerExtended = RecordSoftRateLimit(account)
	require.Equal(t, 2*softRateBase, cooldown)
	require.Nil(t, breakerExtended)

	// 第三次：streak=3 → 2400s；熔断计数达到阈值 → 返回熔断截止时刻。
	cooldown, breakerExtended = RecordSoftRateLimit(account)
	require.Equal(t, 4*softRateBase, cooldown)
	require.NotNil(t, breakerExtended)
}

func TestRecordSchedulerSuccess_ClearsState(t *testing.T) {
	account := codebuddyBackoffAccount()
	RecordSoftRateLimit(account)
	RecordSoftRateLimit(account)
	require.True(t, SchedulerBackoffPresent(account))

	RecordSchedulerSuccess(account)
	require.False(t, SchedulerBackoffPresent(account))
	_, ok := account.Extra[schedulerBackoffExtraKey]
	require.False(t, ok, "success 应删除 extra 退避键")

	// 清零后重新开始，streak 回到 1 → 基数冷却。
	cooldown, _ := RecordSoftRateLimit(account)
	require.Equal(t, softRateBase, cooldown)
}

// ── 三因子加权选号 ────────────────────────────────────────────────────────

func weightedTestSettings() *SchedulerWeightedSelectionSettings {
	s := DefaultSchedulerWeightedSelectionSettings()
	s.Enabled = true
	s.MinPickGapMs = 0 // 关闭防惊群，专注权重
	return s
}

func TestSelectBySchedulerWeight_EmptyAndSingle(t *testing.T) {
	require.Nil(t, SelectBySchedulerWeight(nil, weightedTestSettings(), nil, nil))
	require.Nil(t, SelectBySchedulerWeight([]*Account{}, weightedTestSettings(), nil, nil))

	only := &Account{ID: 7, Platform: PlatformCodebuddy}
	require.Same(t, only, SelectBySchedulerWeight([]*Account{only}, weightedTestSettings(), nil, nil))
}

// 三因子权重本身是确定性的，直接断言 schedulerWeightOf 的输出，
// 不靠采样分布 —— 概率断言在权重差较小（如成功率 +3 vs +0）时不稳定，
// 会在 CI 上随机翻红。选号的随机性单独由 TestSelectBySchedulerWeight_TopNLimit
// 与防惊群用例覆盖（那两处是「必须/必须不」的确定性边界，不依赖概率）。

func TestSchedulerWeightOf_CreditsRatio(t *testing.T) {
	settings := weightedTestSettings()
	now := time.Now()
	// 两号都从未使用（idle 满分）、无统计（中性 1.5），只有 credits 不同。
	high := &Account{ID: 1, Platform: PlatformCodebuddy,
		Extra: map[string]any{"codebuddy_credit": map[string]any{"remain": int64(1000)}}}
	low := &Account{ID: 2, Platform: PlatformCodebuddy,
		Extra: map[string]any{"codebuddy_credit": map[string]any{"remain": int64(10)}}}

	wHigh := schedulerWeightOf(high, 1000, schedulerStats{}, settings, now)
	wLow := schedulerWeightOf(low, 1000, schedulerStats{}, settings, now)
	// 基数 1 + credits占比×10 + idle 5 + 中性 1.5
	require.InDelta(t, 1+10+5+1.5, wHigh, 1e-9, "满分 credits 应拿满 ×10")
	require.InDelta(t, 1+0.1+5+1.5, wLow, 1e-9, "低 credits 按占比折算")
	require.Greater(t, wHigh, wLow)
}

func TestSchedulerWeightOf_SuccessRate(t *testing.T) {
	settings := weightedTestSettings()
	now := time.Now()
	a := &Account{ID: 1, Platform: PlatformCodebuddy,
		Extra: map[string]any{"codebuddy_credit": map[string]any{"remain": int64(100)}}}
	b := &Account{ID: 2, Platform: PlatformCodebuddy,
		Extra: map[string]any{"codebuddy_credit": map[string]any{"remain": int64(100)}}}
	perfect := schedulerStats{SuccessCount: 100, ErrTotal: 0}
	failing := schedulerStats{SuccessCount: 0, ErrTotal: 100}

	wPerfect := schedulerWeightOf(a, 100, perfect, settings, now)
	wFailing := schedulerWeightOf(b, 100, failing, settings, now)
	// 成功率 1.0 → +3；成功率 0 → +0。
	require.InDelta(t, wPerfect-wFailing, 3.0, 1e-9, "成功率因子满值应为 +3")
}

func TestSchedulerWeightOf_NoStatsGivesNeutral(t *testing.T) {
	settings := weightedTestSettings()
	now := time.Now()
	a := &Account{ID: 1, Platform: PlatformCodebuddy,
		Extra: map[string]any{"codebuddy_credit": map[string]any{"remain": int64(100)}}}
	// 无记录 → 中性 1.5，而非 0（否则新号会被系统性压低）。
	neutral := schedulerWeightOf(a, 100, schedulerStats{}, settings, now)
	zeroRate := schedulerWeightOf(a, 100, schedulerStats{SuccessCount: 0, ErrTotal: 1}, settings, now)
	require.InDelta(t, neutral-zeroRate, 1.5, 1e-9, "无记录应给中性 1.5")
}

func TestSchedulerWeightOf_IdleBonus(t *testing.T) {
	settings := weightedTestSettings()
	now := time.Now()
	never := &Account{ID: 1, Platform: PlatformCodebuddy, LastUsedAt: nil,
		Extra: map[string]any{"codebuddy_credit": map[string]any{"remain": int64(100)}}}
	justUsed := &Account{ID: 2, Platform: PlatformCodebuddy, LastUsedAt: testTimePtr(now),
		Extra: map[string]any{"codebuddy_credit": map[string]any{"remain": int64(100)}}}

	wNever := schedulerWeightOf(never, 100, schedulerStats{}, settings, now)
	wJust := schedulerWeightOf(justUsed, 100, schedulerStats{}, settings, now)
	require.InDelta(t, wNever-wJust, settings.IdleWeightMax, 1e-9, "从未使用应拿满 idle 补偿")

	// 闲置封顶：100 小时的闲置不应超过 IdleWeightMax。
	old := &Account{ID: 3, Platform: PlatformCodebuddy,
		LastUsedAt: testTimePtr(now.Add(-100 * time.Hour)),
		Extra:      map[string]any{"codebuddy_credit": map[string]any{"remain": int64(100)}}}
	require.InDelta(t, schedulerWeightOf(old, 100, schedulerStats{}, settings, now), wNever, 1e-9,
		"idle 补偿应封顶在 IdleWeightMax")
}

func TestSelectBySchedulerWeight_TopNLimit(t *testing.T) {
	settings := weightedTestSettings()
	settings.TopN = 2
	// 造 5 个账号，credits 递减，使 rank1/rank2 权重远高于其余。TopN=2 时
	// 只有前两名有机会中选。
	var accounts []*Account
	for i := 0; i < 5; i++ {
		accounts = append(accounts, &Account{
			ID:       int64(i + 1),
			Platform: PlatformCodebuddy,
			Extra:    map[string]any{"codebuddy_credit": map[string]any{"remain": int64((5 - i) * 100)}},
		})
	}
	seen := map[int64]bool{}
	for i := 0; i < 300; i++ {
		seen[SelectBySchedulerWeight(accounts, settings, nil, nil).ID] = true
	}
	require.True(t, seen[1] && seen[2], "Top-N 前两名应都被选到")
	require.False(t, seen[3] || seen[4] || seen[5], "超出 Top-N 的账号不应中选")
}

func TestSelectBySchedulerWeight_MinPickGapSkipsRecent(t *testing.T) {
	settings := weightedTestSettings()
	settings.MinPickGapMs = 5000 // 5s gap
	now := time.Now()
	recentPick := func(id int64) (time.Time, bool) {
		if id == 1 {
			return now, true // 账号 1 刚被选中且在 gap 内
		}
		return time.Time{}, false
	}
	a := &Account{ID: 1, Platform: PlatformCodebuddy,
		Extra: map[string]any{"codebuddy_credit": map[string]any{"remain": int64(1000)}}}
	b := &Account{ID: 2, Platform: PlatformCodebuddy,
		Extra: map[string]any{"codebuddy_credit": map[string]any{"remain": int64(900)}}}

	// 高权重的账号 1 在 gap 内应被跳过，稳定落到账号 2。
	for i := 0; i < 30; i++ {
		require.Equal(t, int64(2), SelectBySchedulerWeight([]*Account{a, b}, settings, recentPick, nil).ID)
	}
}

func TestSelectBySchedulerWeight_MinPickGapAllInGapFallsBack(t *testing.T) {
	settings := weightedTestSettings()
	settings.MinPickGapMs = 5000
	now := time.Now()
	allRecent := func(int64) (time.Time, bool) { return now, true }
	a := &Account{ID: 1, Platform: PlatformCodebuddy}
	b := &Account{ID: 2, Platform: PlatformCodebuddy}
	// Top-N 全部命中 gap：不清空，退化加权随机，仍应返回一个账号而非 nil。
	picked := SelectBySchedulerWeight([]*Account{a, b}, settings, allRecent, nil)
	require.NotNil(t, picked)
}

// ── runtime: setting TTL 缓存 + 进程内状态 ────────────────────────────────

// weightedSelectionSettingRepo 记录 GetValue 调用次数的最小 SettingRepository。
type weightedSelectionSettingRepo struct {
	values        map[string]string
	getValueCalls int
}

func (r *weightedSelectionSettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	return nil, ErrSettingNotFound
}
func (r *weightedSelectionSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.getValueCalls++
	if v, ok := r.values[key]; ok {
		return v, nil
	}
	return "", ErrSettingNotFound
}
func (r *weightedSelectionSettingRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
func (r *weightedSelectionSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, nil
}
func (r *weightedSelectionSettingRepo) SetMultiple(context.Context, map[string]string) error {
	return nil
}
func (r *weightedSelectionSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return nil, nil
}
func (r *weightedSelectionSettingRepo) Delete(_ context.Context, key string) error {
	delete(r.values, key)
	return nil
}

func TestSchedulerWeightedRuntime_NilSafe(t *testing.T) {
	var r *schedulerWeightedRuntime
	require.False(t, r.enabled(nil))
	require.Nil(t, r.lastPickFunc())
	require.Equal(t, schedulerStats{}, r.statsOf(1))
	require.NotPanics(t, func() {
		r.notePick(1)
		r.recordRequestOutcome(1, true)
		r.resetStats(1)
	})
}

func TestSchedulerWeightedRuntime_NotePickAndStats(t *testing.T) {
	r := newSchedulerWeightedRuntime(nil) // settingService nil → 默认关闭值
	r.notePick(42)
	last, ok := r.lastPickFunc()(42)
	require.True(t, ok)
	require.False(t, last.IsZero())
	_, ok = r.lastPickFunc()(99)
	require.False(t, ok, "未记录账号不应有 lastPick")

	r.recordRequestOutcome(42, true)
	r.recordRequestOutcome(42, true)
	r.recordRequestOutcome(42, false)
	require.Equal(t, schedulerStats{SuccessCount: 2, ErrTotal: 1}, r.statsOf(42))

	r.resetStats(42)
	require.Equal(t, schedulerStats{}, r.statsOf(42))
}

func TestSchedulerWeightedRuntime_EnabledReflectsSettings(t *testing.T) {
	repo := &weightedSelectionSettingRepo{values: map[string]string{}}
	svc := &SettingService{settingRepo: repo}
	r := newSchedulerWeightedRuntime(svc)
	require.False(t, r.enabled(nil), "缺省默认关闭")

	require.NoError(t, svc.SetSchedulerWeightedSelectionSettings(nil, &SchedulerWeightedSelectionSettings{Enabled: true}))
	// TTL 内旧缓存仍为关闭；强制失效后读到开启。
	require.False(t, r.enabled(nil), "TTL 内应读到陈旧缓存")
	r.cached.Store(nil)
	require.True(t, r.enabled(nil), "缓存失效后应读到开启")
}

func TestSchedulerWeightedRuntime_SettingsCacheTTL(t *testing.T) {
	repo := &weightedSelectionSettingRepo{values: map[string]string{}}
	r := newSchedulerWeightedRuntime(&SettingService{settingRepo: repo})

	// 首次读缓存 miss，走 repo。第二次读命中缓存，repo 不再被访问。
	_ = r.settings(nil)
	before := repo.getValueCalls
	require.NotNil(t, r.settings(nil))
	require.Equal(t, before, repo.getValueCalls, "TTL 内命中缓存不应重复读 DB")
}
