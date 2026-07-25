package service

import (
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func TestGrokRateLimitResetAtForAccountWithPolicy_FreeFull24h(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	remaining := int64(0)
	limit := xai.GrokFreeRolling24hTokenLimit
	account := &Account{
		ID:       9001,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"subscription_tier": "free",
		},
		Extra: map[string]any{
			grokBillingExtraKey: &xai.BillingSummary{
				StatusCode:       http.StatusOK,
				MonthlyUpdatedAt: now.Format(time.RFC3339),
				Partial:          false,
				FailedWindows:    nil,
			},
		},
	}
	snapshot := &xai.QuotaSnapshot{
		StatusCode: http.StatusTooManyRequests,
		UpdatedAt:  now.Format(time.RFC3339),
		Tokens: &xai.QuotaWindow{
			Limit:     &limit,
			Remaining: &remaining,
		},
	}
	settings := &OpenAIGrok429ExhaustionSettings{
		Enabled:                  true,
		FreeFullDurationHours:    24,
		FreeFullThresholdPercent: 98,
		NoResetDurationMinutes:   60,
	}

	resetAt, limited := grokRateLimitResetAtForAccountWithPolicy(account, snapshot, now, settings, 0)
	require.True(t, limited)
	require.WithinDuration(t, now.Add(24*time.Hour), resetAt, time.Second)
}

func TestGrokRateLimitResetAtForAccountWithPolicy_NoResetUsesConfiguredMinutes(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	account := &Account{
		ID:       9002,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"subscription_tier": "super",
		},
		Extra: map[string]any{
			grokBillingExtraKey: &xai.BillingSummary{
				Plan:             "SuperGrok",
				UsagePercent:     floatPtr(12),
				StatusCode:       http.StatusOK,
				MonthlyUpdatedAt: now.Format(time.RFC3339),
			},
		},
	}
	snapshot := &xai.QuotaSnapshot{
		StatusCode: http.StatusTooManyRequests,
		UpdatedAt:  now.Format(time.RFC3339),
	}
	settings := &OpenAIGrok429ExhaustionSettings{
		Enabled:                  true,
		FreeFullDurationHours:    24,
		FreeFullThresholdPercent: 98,
		NoResetDurationMinutes:   45,
	}

	resetAt, limited := grokRateLimitResetAtForAccountWithPolicy(account, snapshot, now, settings, 0)
	require.True(t, limited)
	require.WithinDuration(t, now.Add(45*time.Minute), resetAt, time.Second)
}

func TestGrokRateLimitResetAtForAccountWithPolicy_DisabledKeepsShortFallback(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	account := &Account{
		ID:       9003,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"subscription_tier": "free",
		},
	}
	snapshot := &xai.QuotaSnapshot{
		StatusCode: http.StatusTooManyRequests,
		UpdatedAt:  now.Format(time.RFC3339),
	}
	settings := &OpenAIGrok429ExhaustionSettings{
		Enabled:                  false,
		FreeFullDurationHours:    24,
		FreeFullThresholdPercent: 98,
		NoResetDurationMinutes:   60,
	}

	resetAt, limited := grokRateLimitResetAtForAccountWithPolicy(account, snapshot, now, settings, 0)
	require.True(t, limited)
	require.WithinDuration(t, now.Add(grokRateLimitFallbackCooldown), resetAt, time.Second)
}

func TestIsSuccessfulGrokRateLimitRecovery_Requires2xx(t *testing.T) {
	limitedAt := time.Now().Add(-time.Minute)
	resetAt := time.Now().Add(time.Hour)
	account := &Account{
		ID:               9004,
		Platform:         PlatformGrok,
		Type:             AccountTypeOAuth,
		RateLimitedAt:    &limitedAt,
		RateLimitResetAt: &resetAt,
	}
	require.True(t, isSuccessfulGrokRateLimitRecovery(account, &xai.QuotaSnapshot{StatusCode: http.StatusOK}))
	require.False(t, isSuccessfulGrokRateLimitRecovery(account, &xai.QuotaSnapshot{StatusCode: http.StatusTooManyRequests}))
}

func floatPtr(v float64) *float64 { return &v }
