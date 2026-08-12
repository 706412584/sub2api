package service

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"
	"time"
)

// Base warm window: refresh when access token lifetime remaining is below this.
// Grok access tokens are typically ~1h; refreshing up to 1h early keeps the pool
// warm for request path cache misses.
const grokTokenRefreshSkew = time.Hour

// 后台刷新分散窗口：在 [expiresAt-refreshWindow, expiresAt-5m] 内按账号稳定抖动，
// 避免大量 Grok 账号同时到期时的刷新惊群。请求路径仍走 NeedsRefresh 的即时门控。
const grokTokenRefreshJitterSpread = 45 * time.Minute

// Stampede spread: each account's effective warm window is reduced by a
// deterministic offset in [0, grokTokenRefreshJitterMax] so co-imported accounts
// do not all refresh in the same TokenRefreshService cycle (grok2api-style
// RefreshDueAt scatter).
const grokTokenRefreshJitterMax = 3 * time.Minute

// Floor so jitter cannot shrink the window below a useful threshold.
const grokTokenRefreshSkewMin = 30 * time.Minute

type GrokTokenRefresher struct {
	grokOAuthService GrokOAuthTokenService
}

func NewGrokTokenRefresher(grokOAuthService GrokOAuthTokenService) *GrokTokenRefresher {
	return &GrokTokenRefresher{grokOAuthService: grokOAuthService}
}

func (r *GrokTokenRefresher) CacheKey(account *Account) string {
	return GrokTokenCacheKey(account)
}

func accountHasGrokSSO(account *Account) bool {
	return account != nil && strings.TrimSpace(account.GetExtraString("sso")) != ""
}

func (r *GrokTokenRefresher) CanRefresh(account *Account) bool {
	if account == nil || account.Platform != PlatformGrok || account.Type != AccountTypeOAuth {
		return false
	}
	return strings.TrimSpace(account.GetGrokRefreshToken()) != "" || accountHasGrokSSO(account)
}

func (r *GrokTokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	if account == nil {
		return false
	}
	hasRT := strings.TrimSpace(account.GetGrokRefreshToken()) != ""
	if !hasRT && !accountHasGrokSSO(account) {
		return false
	}
	if strings.TrimSpace(account.GetGrokAccessToken()) == "" {
		return true
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil {
		return true
	}
	if refreshWindow < grokTokenRefreshSkew {
		refreshWindow = grokTokenRefreshSkew
	}
	// Deterministic per-account jitter: spread warm refreshes without random
	// non-determinism in tests (hash of account id).
	refreshWindow = grokTokenRefreshWindowWithJitter(account.ID, refreshWindow)
	return time.Until(*expiresAt) < refreshWindow
}

// RefreshGrokAccountTokenWithSSOFallback first tries refresh_token, then falls
// back to extra.sso → ConvertFromSSO when present. Used by admin refresh and
// background token refresh so both paths share the same recovery behavior.
func RefreshGrokAccountTokenWithSSOFallback(ctx context.Context, svc GrokOAuthTokenService, account *Account) (*GrokTokenInfo, error) {
	if svc == nil {
		return nil, errors.New("grok oauth service is not configured")
	}
	if account == nil {
		return nil, errors.New("account is nil")
	}

	tokenInfo, err := svc.RefreshAccountToken(ctx, account)
	if err == nil {
		return tokenInfo, nil
	}

	sso := strings.TrimSpace(account.GetExtraString("sso"))
	if sso == "" {
		return nil, err
	}

	ssoInfo, ssoErr := svc.ConvertFromSSO(ctx, sso, account.ProxyID)
	if ssoErr != nil {
		return nil, fmt.Errorf("%w; sso fallback failed: %v", err, ssoErr)
	}
	slog.Info("grok refresh recovered via sso fallback",
		"account_id", account.ID,
		"refresh_err", err.Error(),
	)
	return ssoInfo, nil
}

// grokBackgroundRefreshReady 判断后台刷新是否已到该账号的抖动 due-at。
// 请求路径请继续只用 NeedsRefresh；后台在 NeedsRefresh 之后再调本函数。
func grokBackgroundRefreshReady(account *Account, refreshWindow time.Duration, now time.Time) bool {
	if account == nil {
		return false
	}
	if strings.TrimSpace(account.GetGrokAccessToken()) == "" {
		return true
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil {
		return true
	}
	if refreshWindow < grokTokenRefreshSkew {
		refreshWindow = grokTokenRefreshSkew
	}
	if now.IsZero() {
		now = time.Now()
	}
	until := expiresAt.Sub(now)
	if until >= refreshWindow {
		return false
	}
	// 剩余不足 5 分钟：强制刷新，不再等抖动。
	if until <= 5*time.Minute {
		return true
	}
	return !now.Before(grokTokenRefreshDueAt(account, *expiresAt, refreshWindow))
}

// grokTokenRefreshDueAt 返回该账号后台刷新的最早时刻（稳定哈希抖动）。
func grokTokenRefreshDueAt(account *Account, expiresAt time.Time, refreshWindow time.Duration) time.Time {
	windowStart := expiresAt.Add(-refreshWindow)
	windowEnd := expiresAt.Add(-5 * time.Minute)
	if !windowEnd.After(windowStart) {
		return windowStart
	}
	spread := windowEnd.Sub(windowStart)
	if spread > grokTokenRefreshJitterSpread {
		spread = grokTokenRefreshJitterSpread
		windowStart = windowEnd.Add(-spread)
	}
	h := fnv.New32a()
	var id int64
	if account != nil {
		id = account.ID
	}
	_, _ = h.Write([]byte("grok-refresh-jitter"))
	_, _ = h.Write([]byte{
		byte(id), byte(id >> 8), byte(id >> 16), byte(id >> 24),
		byte(id >> 32), byte(id >> 40), byte(id >> 48), byte(id >> 56),
	})
	off := time.Duration(h.Sum32()) % spread
	if spread <= 0 {
		return windowStart
	}
	return windowStart.Add(off)
}

// grokTokenRefreshWindowWithJitter returns refreshWindow minus a stable offset
// in [0, jitterMax] based on accountID. Result is never below grokTokenRefreshSkewMin
// when the base window is at least that large.
func grokTokenRefreshWindowWithJitter(accountID int64, refreshWindow time.Duration) time.Duration {
	if accountID <= 0 || refreshWindow <= grokTokenRefreshSkewMin {
		return refreshWindow
	}
	h := fnv.New32a()
	var b [8]byte
	id := uint64(accountID)
	for i := 0; i < 8; i++ {
		b[i] = byte(id >> (8 * i))
	}
	_, _ = h.Write(b[:])
	// Jitter in [0, grokTokenRefreshJitterMax).
	jitter := time.Duration(h.Sum32()%uint32(grokTokenRefreshJitterMax/time.Second)) * time.Second
	out := refreshWindow - jitter
	if out < grokTokenRefreshSkewMin {
		return grokTokenRefreshSkewMin
	}
	return out
}

func (r *GrokTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	if r == nil || r.grokOAuthService == nil {
		return nil, errors.New("grok oauth service is not configured")
	}
	tokenInfo, err := RefreshGrokAccountTokenWithSSOFallback(ctx, r.grokOAuthService, account)
	if err != nil {
		return nil, err
	}
	newCredentials := r.grokOAuthService.BuildAccountCredentials(tokenInfo)
	newCredentials = MergeCredentials(account.Credentials, newCredentials)
	if baseURL := strings.TrimSpace(account.GetCredential("base_url")); baseURL != "" {
		newCredentials["base_url"] = baseURL
	}
	return newCredentials, nil
}
