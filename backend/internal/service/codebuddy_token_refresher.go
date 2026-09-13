package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// CodebuddyTokenInfo 刷新响应中需要回写的 token 字段。
type CodebuddyTokenInfo struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
	Domain       string
}

// CodebuddyTokenRefresher 处理 CodeBuddy 两种账号类型（CN/Global）的 token 刷新。
// 刷新端点 POST /v2/plugin/auth/token/refresh（chatBase 域），凭 X-Refresh-Token 头鉴权。
type CodebuddyTokenRefresher struct {
	httpUpstream        HTTPUpstream
	tlsFPProfileService *TLSFingerprintProfileService
}

func NewCodebuddyTokenRefresher(httpUpstream HTTPUpstream, tlsFPProfileService *TLSFingerprintProfileService) *CodebuddyTokenRefresher {
	return &CodebuddyTokenRefresher{
		httpUpstream:        httpUpstream,
		tlsFPProfileService: tlsFPProfileService,
	}
}

// CacheKey 返回用于分布式锁的缓存键。
func (r *CodebuddyTokenRefresher) CacheKey(account *Account) string {
	return fmt.Sprintf("codebuddy:%d", account.ID)
}

// CanRefresh 检查是否能处理此账号：两个 CodeBuddy 账号类型均支持。
func (r *CodebuddyTokenRefresher) CanRefresh(account *Account) bool {
	return account != nil && account.IsCodebuddy() && codebuddy.IsAccountType(account.Type)
}

// NeedsRefresh 检查 token 是否需要刷新。
// 无过期时间视为需要刷新（Kiro 同语义）；过期时间由 expires_at 判定。
func (r *CodebuddyTokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	if !r.CanRefresh(account) {
		return false
	}
	creds := codebuddy.FromCredentialsMap(account.Credentials)
	if !creds.ShouldRefresh() {
		return false
	}
	return creds.NeedsRefresh(refreshWindow)
}

// Refresh 执行 token 刷新，返回更新后的 credentials。
// 保留原有字段，只更新 token 相关字段；响应缺省值保留旧值（防刷新风暴）。
func (r *CodebuddyTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	tokenInfo, err := r.refreshToken(ctx, account)
	if err != nil {
		return nil, err
	}
	return MergeCredentials(account.Credentials, buildCodebuddyTokenCredentials(tokenInfo, account)), nil
}

// refreshToken 调用上游刷新端点并解析响应。
func (r *CodebuddyTokenRefresher) refreshToken(ctx context.Context, account *Account) (*CodebuddyTokenInfo, error) {
	creds := codebuddy.FromCredentialsMap(account.Credentials)
	if !creds.ShouldRefresh() {
		return nil, fmt.Errorf("codebuddy: no refresh token available")
	}
	region := codebuddy.RegionForAccountType(account.Type)
	req, err := codebuddy.BuildRefreshRequest(creds, region, codebuddy.EndpointOptions{})
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileOpenAI))
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	var tlsProfile = (*tlsfingerprint.Profile)(nil)
	if r.tlsFPProfileService != nil {
		tlsProfile = r.tlsFPProfileService.ResolveTLSProfile(account)
	}
	if r.httpUpstream == nil {
		return nil, fmt.Errorf("codebuddy: http upstream is not configured")
	}
	resp, err := r.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return nil, readErr
	}
	data, err := codebuddy.ParseEnvelope(resp.StatusCode, respBody)
	if err != nil {
		return nil, err
	}
	var tok struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		Domain       string `json:"domain"`
	}
	if err := json.Unmarshal(data, &tok); err != nil || strings.TrimSpace(tok.AccessToken) == "" {
		return nil, fmt.Errorf("codebuddy: refresh failed, no accessToken in response — re-login required")
	}
	return &CodebuddyTokenInfo{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresIn:    tok.ExpiresIn,
		Domain:       tok.Domain,
	}, nil
}

// buildCodebuddyTokenCredentials 构建刷新后的 token 凭证键。
// preserveExpiry：响应缺 expiresIn 时保留旧 expires_at，避免刷新风暴。
func buildCodebuddyTokenCredentials(tokenInfo *CodebuddyTokenInfo, account *Account) map[string]any {
	credentials := map[string]any{
		"access_token": tokenInfo.AccessToken,
	}
	if strings.TrimSpace(tokenInfo.RefreshToken) != "" {
		credentials["refresh_token"] = tokenInfo.RefreshToken
	}
	if tokenInfo.ExpiresIn > 0 {
		credentials["expires_at"] = time.Now().Add(time.Duration(tokenInfo.ExpiresIn) * time.Second).Unix()
		credentials["expires_in"] = tokenInfo.ExpiresIn
	}
	if strings.TrimSpace(tokenInfo.Domain) != "" {
		credentials["domain"] = tokenInfo.Domain
	}
	return credentials
}

// CodebuddySessionDeadError 表示会话已失效（12153 / Offline user session not found），
// 需要人工重新登录，重试无意义。
type CodebuddySessionDeadError struct {
	Msg string
}

func (e *CodebuddySessionDeadError) Error() string {
	return "codebuddy session dead (re-login required): " + e.Msg
}
