//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/stretchr/testify/require"
)

func codebuddyRefreshTestAccount() *Account {
	return &Account{
		ID:       7,
		Platform: PlatformCodebuddy,
		Type:     codebuddy.AccountTypeCN,
		Credentials: map[string]any{
			"access_token":  "old-access",
			"refresh_token": "rt-old",
			"expires_at":    float64(time.Now().Add(-time.Hour).Unix()),
			"uid":           "u-1",
			"enterprise_id": "e-1",
			"domain":        "domain-old",
		},
	}
}

func codebuddyRefreshResponse(body string, status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestCodebuddyTokenRefresherCanAndNeedsRefresh(t *testing.T) {
	r := NewCodebuddyTokenRefresher(nil, nil)

	cnAccount := codebuddyRefreshTestAccount()
	require.True(t, r.CanRefresh(cnAccount))
	require.True(t, r.NeedsRefresh(cnAccount, time.Hour), "已过期 token 必须判定需要刷新")

	globalAccount := codebuddyRefreshTestAccount()
	globalAccount.Type = codebuddy.AccountTypeGlobal
	require.True(t, r.CanRefresh(globalAccount), "Global 账号同样可刷新")

	futureAccount := codebuddyRefreshTestAccount()
	futureAccount.Credentials["expires_at"] = float64(time.Now().Add(24 * time.Hour).Unix())
	require.False(t, r.NeedsRefresh(futureAccount, time.Hour), "未进入刷新窗口不刷新")

	noRefreshToken := codebuddyRefreshTestAccount()
	delete(noRefreshToken.Credentials, "refresh_token")
	require.False(t, r.NeedsRefresh(noRefreshToken, time.Hour), "无 refresh_token 不能刷新")

	nonCodebuddy := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	require.False(t, r.CanRefresh(nonCodebuddy))
}

func TestCodebuddyTokenRefresherRefreshSuccess(t *testing.T) {
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		codebuddyRefreshResponse(`{"code":0,"msg":"ok","data":{"accessToken":"new-access","refreshToken":"rt-new","expiresIn":3600,"domain":"domain-new"}}`, http.StatusOK),
	}}
	r := NewCodebuddyTokenRefresher(upstream, nil)
	account := codebuddyRefreshTestAccount()

	creds, err := r.Refresh(context.Background(), account)
	require.NoError(t, err)

	require.Len(t, upstream.requests, 1)
	req := upstream.requests[0]
	// 刷新端点：X-Refresh-Token 必须携带；Authorization 也应已更新前的 access
	require.Equal(t, "rt-old", req.Header.Get("X-Refresh-Token"))
	require.Contains(t, req.URL.Path, "/v2/plugin/auth/token/refresh")
	// CN 账号必须打到 copilot.tencent.com
	require.Equal(t, "copilot.tencent.com", req.URL.Host)

	// 回写字段：token/refresh/domain/expires 全部更新
	require.Equal(t, "new-access", creds["access_token"])
	require.Equal(t, "rt-new", creds["refresh_token"])
	require.Equal(t, "domain-new", creds["domain"])
	require.Equal(t, int64(3600), creds["expires_in"])
	expiresAt, ok := creds["expires_at"].(int64)
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(time.Hour), time.Unix(expiresAt, 0), time.Minute)

	// 保留原有非 token 字段
	require.Equal(t, "u-1", creds["uid"])
	require.Equal(t, "e-1", creds["enterprise_id"])
}

func TestCodebuddyTokenRefresherRefreshPreservesOmittedFields(t *testing.T) {
	// 响应缺 refreshToken / expiresIn / domain 时保留旧值（防刷新风暴）
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		codebuddyRefreshResponse(`{"code":0,"msg":"ok","data":{"accessToken":"new-access"}}`, http.StatusOK),
	}}
	r := NewCodebuddyTokenRefresher(upstream, nil)
	account := codebuddyRefreshTestAccount()
	oldExpires := account.Credentials["expires_at"]

	creds, err := r.Refresh(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "rt-old", creds["refresh_token"], "响应缺 refreshToken 保留旧值")
	require.Equal(t, oldExpires, creds["expires_at"], "响应缺 expiresIn 保留旧过期时间")
	require.Equal(t, "domain-old", creds["domain"], "响应缺 domain 保留旧值")
}

func TestCodebuddyTokenRefresherRefreshGlobalRegion(t *testing.T) {
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		codebuddyRefreshResponse(`{"code":0,"msg":"ok","data":{"accessToken":"g-access"}}`, http.StatusOK),
	}}
	r := NewCodebuddyTokenRefresher(upstream, nil)
	account := codebuddyRefreshTestAccount()
	account.Type = codebuddy.AccountTypeGlobal

	_, err := r.Refresh(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "www.workbuddy.ai", upstream.requests[0].URL.Host, "Global 账号刷新打到 workbuddy.ai")
}

func TestCodebuddyTokenRefresherRefreshSessionDead(t *testing.T) {
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		codebuddyRefreshResponse(`{"code":12153,"msg":"Offline user session not found"}`, http.StatusOK),
	}}
	r := NewCodebuddyTokenRefresher(upstream, nil)
	account := codebuddyRefreshTestAccount()

	creds, err := r.Refresh(context.Background(), account)
	require.Error(t, err)
	require.Nil(t, creds)
	require.Contains(t, err.Error(), "session_dead", "12153 必须分类为 session_dead")
}

func TestCodebuddyTokenRefresherRefreshNoAccessToken(t *testing.T) {
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		codebuddyRefreshResponse(`{"code":0,"msg":"ok","data":{}}`, http.StatusOK),
	}}
	r := NewCodebuddyTokenRefresher(upstream, nil)
	account := codebuddyRefreshTestAccount()

	_, err := r.Refresh(context.Background(), account)
	require.Error(t, err)
	require.Contains(t, err.Error(), "re-login required")
}

func TestCodebuddyTokenRefresherRefreshWithoutRefreshToken(t *testing.T) {
	r := NewCodebuddyTokenRefresher(nil, nil)
	account := codebuddyRefreshTestAccount()
	delete(account.Credentials, "refresh_token")

	_, err := r.Refresh(context.Background(), account)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no refresh token available")
}

func TestTokenRefreshServiceCodebuddyDepsInjection(t *testing.T) {
	// SetCodebuddyDeps 必须把网络依赖注入到已注册的 CodeBuddy 刷新器
	svc := NewTokenRefreshService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	svc.SetCodebuddyDeps(&queuedHTTPUpstream{}, nil)

	var registration *tokenRefreshRegistration
	for i := range svc.registrations {
		if svc.registrations[i].platform == PlatformCodebuddy {
			registration = &svc.registrations[i]
		}
	}
	require.NotNil(t, registration, "CodeBuddy 必须已注册")
	refresher, ok := registration.refresher.(*CodebuddyTokenRefresher)
	require.True(t, ok)
	require.NotNil(t, refresher.httpUpstream, "HTTPUpstream 必须已注入")
	require.Equal(t, refresher, registration.executor, "executor 必须指向同一刷新器实例")
}
