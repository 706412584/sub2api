package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func openAIResetTestScheduler(reset float64) *defaultOpenAIAccountScheduler {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.SchedulerScoreWeights = config.GatewayOpenAIWSSchedulerScoreWeights{
		Priority:      1.0,
		Load:          1.0,
		Queue:         0.7,
		ErrorRate:     0.8,
		TTFT:          0.5,
		Reset:         reset,
		QuotaHeadroom: 0,
	}
	return &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{cfg: cfg}}
}

func openAIQuotaHeadroomTestScheduler(quotaHeadroom float64) *defaultOpenAIAccountScheduler {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.SchedulerScoreWeights = config.GatewayOpenAIWSSchedulerScoreWeights{
		QuotaHeadroom: quotaHeadroom,
	}
	return &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{cfg: cfg}}
}

func openAIPlanScores(plan openAIAccountLoadPlan) map[int64]float64 {
	scores := make(map[int64]float64, len(plan.candidates))
	for _, c := range plan.candidates {
		scores[c.account.ID] = c.score
	}
	return scores
}

// Reset 权重 > 0 时，会话窗口最早重置的账号应获得更高分。
func TestBuildOpenAIAccountLoadPlan_ResetWeightPrefersSoonestReset(t *testing.T) {
	now := time.Now()
	soon := now.Add(1 * time.Hour)
	later := now.Add(20 * time.Hour)
	filtered := []*Account{
		{ID: 1, Priority: 0, SessionWindowEnd: &later},
		{ID: 2, Priority: 0, SessionWindowEnd: &soon},
	}
	sched := openAIResetTestScheduler(5.0)

	plan := sched.buildOpenAIAccountLoadPlan(context.Background(), OpenAIAccountScheduleRequest{}, filtered, map[int64]*AccountLoadInfo{})
	scores := openAIPlanScores(plan)
	require.Greater(t, scores[2], scores[1], "重置时间最早的账号（ID=2）得分更高")
}

// Reset 权重为 0（默认）时，窗口重置时间不应影响打分，保持原有行为。
func TestBuildOpenAIAccountLoadPlan_ResetWeightZeroNoEffect(t *testing.T) {
	now := time.Now()
	soon := now.Add(1 * time.Hour)
	later := now.Add(20 * time.Hour)
	filtered := []*Account{
		{ID: 1, Priority: 0, SessionWindowEnd: &later},
		{ID: 2, Priority: 0, SessionWindowEnd: &soon},
	}
	sched := openAIResetTestScheduler(0.0)

	plan := sched.buildOpenAIAccountLoadPlan(context.Background(), OpenAIAccountScheduleRequest{}, filtered, map[int64]*AccountLoadInfo{})
	scores := openAIPlanScores(plan)
	require.Equal(t, scores[1], scores[2], "Reset 权重为 0 时两账号得分相同")
}

// 无活跃窗口的账号 reset 因子为 0，应低于拥有未来窗口的账号。
func TestBuildOpenAIAccountLoadPlan_ResetWeightIgnoresNilWindow(t *testing.T) {
	now := time.Now()
	soon := now.Add(2 * time.Hour)
	filtered := []*Account{
		{ID: 1, Priority: 0, SessionWindowEnd: nil},
		{ID: 2, Priority: 0, SessionWindowEnd: &soon},
	}
	sched := openAIResetTestScheduler(5.0)

	plan := sched.buildOpenAIAccountLoadPlan(context.Background(), OpenAIAccountScheduleRequest{}, filtered, map[int64]*AccountLoadInfo{})
	scores := openAIPlanScores(plan)
	require.Greater(t, scores[2], scores[1], "拥有活跃窗口的账号得分高于无窗口账号")
}

func TestOpenAIQuotaHeadroomFactor_PrimaryUsedPercent(t *testing.T) {
	now := time.Date(2026, 3, 11, 10, 0, 0, 0, time.UTC)
	account := &Account{
		Extra: map[string]any{
			"codex_primary_used_percent": 20.0,
			"codex_primary_reset_at":     now.Add(24 * time.Hour).Format(time.RFC3339),
			"codex_usage_updated_at":     now.Add(-time.Minute).Format(time.RFC3339),
		},
	}

	require.InDelta(t, 0.8, openAIQuotaHeadroomFactor(account, now), 0.0001)
}

func TestOpenAIQuotaHeadroomFactor_PrimaryMissingIsNeutral(t *testing.T) {
	now := time.Date(2026, 3, 11, 10, 0, 0, 0, time.UTC)
	account := &Account{
		Extra: map[string]any{
			"codex_usage_updated_at": now.Add(-time.Minute).Format(time.RFC3339),
		},
	}

	require.Equal(t, openAIQuotaHeadroomNeutralFactor, openAIQuotaHeadroomFactor(account, now))
}

func TestOpenAIQuotaHeadroomFactor_PrimaryResetExpiredIsNeutral(t *testing.T) {
	now := time.Date(2026, 3, 11, 10, 0, 0, 0, time.UTC)
	account := &Account{
		Extra: map[string]any{
			"codex_primary_used_percent": 20.0,
			"codex_primary_reset_at":     now.Add(-time.Minute).Format(time.RFC3339),
			"codex_usage_updated_at":     now.Add(-time.Minute).Format(time.RFC3339),
		},
	}

	require.Equal(t, openAIQuotaHeadroomNeutralFactor, openAIQuotaHeadroomFactor(account, now))
}

func TestOpenAIQuotaHeadroomFactor_SecondaryLowHeadroomDiscountsPrimary(t *testing.T) {
	now := time.Date(2026, 3, 11, 10, 0, 0, 0, time.UTC)
	account := &Account{
		Extra: map[string]any{
			"codex_primary_used_percent":   20.0,
			"codex_primary_reset_at":       now.Add(24 * time.Hour).Format(time.RFC3339),
			"codex_secondary_used_percent": 95.0,
			"codex_secondary_reset_at":     now.Add(time.Hour).Format(time.RFC3339),
			"codex_usage_updated_at":       now.Add(-time.Minute).Format(time.RFC3339),
		},
	}

	require.InDelta(t, 0.4, openAIQuotaHeadroomFactor(account, now), 0.0001)
}

func TestBuildOpenAIAccountLoadPlan_QuotaHeadroomPrefersHigher7dRemaining(t *testing.T) {
	now := time.Now()
	filtered := []*Account{
		{
			ID:       1,
			Priority: 0,
			Extra: map[string]any{
				"codex_primary_used_percent": 80.0,
				"codex_primary_reset_at":     now.Add(24 * time.Hour).Format(time.RFC3339),
				"codex_usage_updated_at":     now.Add(-time.Minute).Format(time.RFC3339),
			},
		},
		{
			ID:       2,
			Priority: 0,
			Extra: map[string]any{
				"codex_primary_used_percent": 20.0,
				"codex_primary_reset_at":     now.Add(24 * time.Hour).Format(time.RFC3339),
				"codex_usage_updated_at":     now.Add(-time.Minute).Format(time.RFC3339),
			},
		},
	}
	sched := openAIQuotaHeadroomTestScheduler(1.0)

	plan := sched.buildOpenAIAccountLoadPlan(context.Background(), OpenAIAccountScheduleRequest{}, filtered, map[int64]*AccountLoadInfo{})
	scores := openAIPlanScores(plan)
	require.Greater(t, scores[2], scores[1], "7d 剩余额度更高的账号得分应更高")
}

func TestBuildOpenAIAccountLoadPlan_QuotaHeadroomZeroNoEffect(t *testing.T) {
	now := time.Now()
	filtered := []*Account{
		{
			ID:       1,
			Priority: 0,
			Extra: map[string]any{
				"codex_primary_used_percent": 80.0,
				"codex_primary_reset_at":     now.Add(24 * time.Hour).Format(time.RFC3339),
				"codex_usage_updated_at":     now.Add(-time.Minute).Format(time.RFC3339),
			},
		},
		{
			ID:       2,
			Priority: 0,
			Extra: map[string]any{
				"codex_primary_used_percent": 20.0,
				"codex_primary_reset_at":     now.Add(24 * time.Hour).Format(time.RFC3339),
				"codex_usage_updated_at":     now.Add(-time.Minute).Format(time.RFC3339),
			},
		},
	}
	sched := openAIResetTestScheduler(0)

	plan := sched.buildOpenAIAccountLoadPlan(context.Background(), OpenAIAccountScheduleRequest{}, filtered, map[int64]*AccountLoadInfo{})
	scores := openAIPlanScores(plan)
	require.Equal(t, scores[1], scores[2], "quota_headroom 权重为 0 时不应影响打分")
}

func grokFreeQuotaInt64(v int64) *int64 { return &v }

func grokFreeTestAccount(id int64, remaining, limit int64, updatedAt time.Time) *Account {
	return &Account{
		ID:       id,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Priority: 0,
		Status:   StatusActive,
		Extra: map[string]any{
			grokQuotaSnapshotExtraKey: &xai.QuotaSnapshot{
				Tokens: &xai.QuotaWindow{
					Limit:     grokFreeQuotaInt64(limit),
					Remaining: grokFreeQuotaInt64(remaining),
				},
				SubscriptionTier: "free",
				StatusCode:       200,
				HeadersObserved:  true,
				UpdatedAt:        updatedAt.UTC().Format(time.RFC3339),
			},
		},
	}
}

func TestPreferGrokAccountsWithFreeHeadroom_DropsExhaustedWhenAlternativesExist(t *testing.T) {
	now := time.Now()
	healthy := grokFreeTestAccount(1, 800_000, xai.GrokFreeRolling24hTokenLimit, now.Add(-time.Minute))
	exhausted := grokFreeTestAccount(2, 0, xai.GrokFreeRolling24hTokenLimit, now.Add(-time.Minute))
	paid := &Account{ID: 3, Platform: PlatformGrok, Type: AccountTypeOAuth, Priority: 0, Status: StatusActive}

	preferred := preferGrokAccountsWithFreeHeadroom([]*Account{healthy, exhausted, paid}, now)
	require.Len(t, preferred, 2)
	ids := []int64{preferred[0].ID, preferred[1].ID}
	require.Contains(t, ids, int64(1))
	require.Contains(t, ids, int64(3))
	require.NotContains(t, ids, int64(2))
}

func TestPreferGrokAccountsWithFreeHeadroom_KeepsAllWhenOnlyExhausted(t *testing.T) {
	now := time.Now()
	a := grokFreeTestAccount(1, 0, xai.GrokFreeRolling24hTokenLimit, now.Add(-time.Minute))
	b := grokFreeTestAccount(2, 0, xai.GrokFreeRolling24hTokenLimit, now.Add(-time.Minute))
	// function itself drops exhausted; caller keeps original when preferred empty/same.
	preferred := preferGrokAccountsWithFreeHeadroom([]*Account{a, b}, now)
	require.Empty(t, preferred)
}

func TestGrokFreeHeadroomFactor_PrefersHigherRemaining(t *testing.T) {
	now := time.Now()
	low := grokFreeTestAccount(1, 50_000, xai.GrokFreeRolling24hTokenLimit, now.Add(-time.Minute))
	high := grokFreeTestAccount(2, 900_000, xai.GrokFreeRolling24hTokenLimit, now.Add(-time.Minute))
	exhausted := grokFreeTestAccount(3, 0, xai.GrokFreeRolling24hTokenLimit, now.Add(-time.Minute))
	paid := &Account{ID: 4, Platform: PlatformGrok, Type: AccountTypeOAuth}

	require.Greater(t, grokFreeHeadroomFactor(high, now), grokFreeHeadroomFactor(low, now))
	require.Equal(t, 0.0, grokFreeHeadroomFactor(exhausted, now))
	require.Equal(t, 1.0, grokFreeHeadroomFactor(paid, now))
	require.Equal(t, 0.0, grokFreeHeadroomFactor(&Account{ID: 9, Platform: PlatformOpenAI}, now))
}

func TestBuildOpenAIAccountLoadPlan_GrokFreeHeadroomPrefersHigherRemaining(t *testing.T) {
	now := time.Now()
	filtered := []*Account{
		grokFreeTestAccount(1, 50_000, xai.GrokFreeRolling24hTokenLimit, now.Add(-time.Minute)),
		grokFreeTestAccount(2, 900_000, xai.GrokFreeRolling24hTokenLimit, now.Add(-time.Minute)),
	}
	// Keep other weights zero so only Grok free headroom differentiates.
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.SchedulerScoreWeights = config.GatewayOpenAIWSSchedulerScoreWeights{}
	sched := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{cfg: cfg}}

	plan := sched.buildOpenAIAccountLoadPlan(context.Background(), OpenAIAccountScheduleRequest{Platform: PlatformGrok}, filtered, map[int64]*AccountLoadInfo{})
	scores := openAIPlanScores(plan)
	require.Greater(t, scores[2], scores[1], "Grok free 剩余额度更高的账号得分应更高")
}

func TestIsGrokFreeQuotaExhaustedForScheduling_StaleSnapshotFailOpen(t *testing.T) {
	now := time.Now()
	stale := grokFreeTestAccount(1, 0, xai.GrokFreeRolling24hTokenLimit, now.Add(-48*time.Hour))
	fresh := grokFreeTestAccount(2, 0, xai.GrokFreeRolling24hTokenLimit, now.Add(-time.Minute))
	require.False(t, isGrokFreeQuotaExhaustedForScheduling(stale, now))
	require.True(t, isGrokFreeQuotaExhaustedForScheduling(fresh, now))
}
