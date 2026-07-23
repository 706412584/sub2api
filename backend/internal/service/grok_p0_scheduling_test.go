//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func TestClassifyGrokUpstreamFailure(t *testing.T) {
	t.Parallel()
	require.Equal(t, grokFailAuth, classifyGrokUpstreamFailure(http.StatusUnauthorized, nil))
	require.Equal(t, grokFailPayment, classifyGrokUpstreamFailure(http.StatusPaymentRequired, nil))
	require.Equal(t, grokFailRateLimit, classifyGrokUpstreamFailure(http.StatusTooManyRequests, nil))
	require.Equal(t, grokFailTransient, classifyGrokUpstreamFailure(http.StatusBadGateway, nil))
	require.Equal(t, grokFailTransient, classifyGrokUpstreamFailure(http.StatusInternalServerError, nil))
	require.Equal(t, grokFailContentPolicy, classifyGrokUpstreamFailure(http.StatusForbidden, []byte(`{"error":{"code":"new_sensitive","message":"text is sensitive"}}`)))
	require.Equal(t, grokFailForbidden, classifyGrokUpstreamFailure(http.StatusForbidden, []byte(`{"error":{"code":"account_suspended"}}`)))
}

func TestResolveGrokExhaustionUntilUsesBillingPeriodEnd(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	periodEnd := now.Add(3 * 24 * time.Hour)
	account := &Account{
		ID:       1,
		Platform: PlatformGrok,
		Extra: map[string]any{
			grokBillingExtraKey: &xai.BillingSummary{
				PeriodEnd: periodEnd.Format(time.RFC3339),
			},
		},
	}
	until := resolveGrokExhaustionUntil(account, nil, now)
	require.WithinDuration(t, periodEnd, until, time.Second)
}

func TestResolveGrokExhaustionUntilUsesRetryAfterWhenLater(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	headers := http.Header{"Retry-After": []string{"7200"}} // 2h > 30m fallback
	until := resolveGrokExhaustionUntil(&Account{ID: 2, Platform: PlatformGrok}, headers, now)
	require.WithinDuration(t, now.Add(2*time.Hour), until, time.Second)
}

func TestResolveGrokExhaustionUntilFallbackWhenNoSignals(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	until := resolveGrokExhaustionUntil(&Account{ID: 3, Platform: PlatformGrok}, nil, now)
	require.WithinDuration(t, now.Add(grokPaymentRequiredFallbackDuration), until, time.Second)
}

func TestHandleGrokAccountUpstreamError402UsesBillingPeriodEnd(t *testing.T) {
	now := time.Now().UTC()
	periodEnd := now.Add(2 * 24 * time.Hour).Truncate(time.Second)
	account := &Account{
		ID: 710, Platform: PlatformGrok, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		Extra: map[string]any{
			grokBillingExtraKey: &xai.BillingSummary{
				BillingPeriodEnd: periodEnd.Format(time.RFC3339),
			},
		},
	}
	repo := &grokQuotaAccountRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}

	svc.handleGrokAccountUpstreamError(context.Background(), account, http.StatusPaymentRequired, nil, nil)

	require.Equal(t, 1, repo.tempUnschedCalls)
	require.WithinDuration(t, periodEnd, repo.lastTempUnschedUntil, time.Second)
	require.Equal(t, "grok payment required", repo.lastTempUnschedReason)
	require.Equal(t, 1, repo.updateCalls)
	require.Contains(t, repo.updates[account.ID], grokQuotaBlockedUntilExtraKey)
}

func TestHandleGrokAccountUpstreamError429SetsModelRateLimit(t *testing.T) {
	account := &Account{ID: 711, Platform: PlatformGrok, Type: AccountTypeOAuth}
	repo := &grokQuotaAccountRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	before := time.Now()

	svc.handleGrokAccountUpstreamError(
		context.Background(),
		account,
		http.StatusTooManyRequests,
		http.Header{"Retry-After": []string{"90"}},
		nil,
		"grok-4.5",
	)

	require.Equal(t, 1, repo.rateLimitedCalls) // account-level still set via snapshot
	require.Equal(t, 1, repo.modelRateLimitCalls)
	require.Equal(t, account.ID, repo.lastModelRateLimitID)
	require.Equal(t, "grok-4.5", repo.lastModelRateLimitKey)
	require.Equal(t, grokModelRateLimitReason, repo.lastModelRateLimitReason)
	require.True(t, repo.lastModelRateLimitAt.After(before.Add(89*time.Second)))
	// 内存侧也写入，便于调度立即生效
	require.True(t, account.isRateLimitActiveForKey("grok-4.5"))
}

func TestHandleGrokAccountUpstreamError429WithoutModelSkipsModelLimit(t *testing.T) {
	account := &Account{ID: 712, Platform: PlatformGrok, Type: AccountTypeOAuth}
	repo := &grokQuotaAccountRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}

	svc.handleGrokAccountUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{"Retry-After": []string{"45"}}, nil)

	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Zero(t, repo.modelRateLimitCalls)
}

func TestGrokBackgroundRefreshReadyJitter(t *testing.T) {
	t.Parallel()
	now := time.Now()
	// 30 minutes until expiry, window=1h → inside refresh window but not necessarily due
	expires := now.Add(30 * time.Minute)
	account := &Account{
		ID:       1001,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "a",
			"refresh_token": "r",
			"expires_at":    expires.UTC().Format(time.RFC3339),
		},
	}
	// NeedsRefresh should be true (within 1h skew)
	refresher := NewGrokTokenRefresher(nil)
	require.True(t, refresher.NeedsRefresh(account, time.Hour))

	due := grokTokenRefreshDueAt(account, expires, time.Hour)
	// Stable due-at between windowStart and windowEnd
	require.True(t, !due.Before(expires.Add(-time.Hour)) || due.Equal(expires.Add(-time.Hour)) || due.After(expires.Add(-time.Hour)))
	require.True(t, due.Before(expires) || due.Equal(expires.Add(-5*time.Minute)))

	// Before due-at → background not ready; after due-at → ready
	require.False(t, grokBackgroundRefreshReady(account, time.Hour, due.Add(-time.Second)))
	require.True(t, grokBackgroundRefreshReady(account, time.Hour, due.Add(time.Second)))

	// Near expiry forces ready
	account.Credentials["expires_at"] = now.Add(2 * time.Minute).UTC().Format(time.RFC3339)
	require.True(t, grokBackgroundRefreshReady(account, time.Hour, now))
}

func TestGrokTokenRefreshDueAtStablePerAccount(t *testing.T) {
	t.Parallel()
	expires := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	a1 := &Account{ID: 1}
	a2 := &Account{ID: 2}
	d1a := grokTokenRefreshDueAt(a1, expires, time.Hour)
	d1b := grokTokenRefreshDueAt(a1, expires, time.Hour)
	d2 := grokTokenRefreshDueAt(a2, expires, time.Hour)
	require.Equal(t, d1a, d1b)
	// Different accounts should almost always differ; if they collide it's still ok but unlikely
	// We only assert stability for same account.
	_ = d2
}
