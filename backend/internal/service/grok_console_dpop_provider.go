package service

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/sync/singleflight"
)

// ConsoleDPoPSession 存放内存中的 DPoP 会话资产
type ConsoleDPoPSession struct {
	AccessToken string
	PrivateKey  *ecdsa.PrivateKey
	PublicJWK   map[string]string
	ExpiresAt   time.Time
}

// GrokConsoleDPoPProvider 管理 Console 的 DPoP 会话（P-256 私钥仅内存，不落库）
type GrokConsoleDPoPProvider struct {
	sessionService  GrokSessionCredentialService
	httpUpstream    HTTPUpstream
	httpClient      *http.Client
	proxyURLResolver func(int64) string
	mu              sync.Mutex
	sessions        map[string]*ConsoleDPoPSession
	loads           singleflight.Group
}

// NewGrokConsoleDPoPProvider 创建 Console DPoP Provider
func NewGrokConsoleDPoPProvider(
	sessionService GrokSessionCredentialService,
	httpUpstream HTTPUpstream,
) *GrokConsoleDPoPProvider {
	return &GrokConsoleDPoPProvider{
		sessionService: sessionService,
		httpUpstream:   httpUpstream,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
		},
		sessions: make(map[string]*ConsoleDPoPSession),
	}
}

// SetHTTPClient 注入自定义 HTTP 客户端（用于代理）
func (p *GrokConsoleDPoPProvider) SetHTTPClient(client *http.Client) {
	if client != nil {
		p.httpClient = client
	}
}

// GetOrCreateSession 获取或创建 DPoP 会话
func (p *GrokConsoleDPoPProvider) GetOrCreateSession(
	ctx context.Context,
	accountID int64,
	proxyID *int64,
) (*ConsoleDPoPSession, error) {
	cacheKey := fmt.Sprintf("console:%d:%v", accountID, proxyID)

	p.mu.Lock()
	if sess, ok := p.sessions[cacheKey]; ok && time.Now().Before(sess.ExpiresAt.Add(-30*time.Second)) {
		p.mu.Unlock()
		return sess, nil
	}
	p.mu.Unlock()

	result, err, _ := p.loads.Do(cacheKey, func() (any, error) {
		return p.createSession(ctx, accountID, proxyID, cacheKey)
	})
	if err != nil {
		return nil, err
	}
	session, ok := result.(*ConsoleDPoPSession)
	if !ok {
		return nil, fmt.Errorf("unexpected session type %T", result)
	}
	return session, nil
}

// createSession 执行 DPoP 换票流程
func (p *GrokConsoleDPoPProvider) createSession(
	ctx context.Context,
	accountID int64,
	proxyID *int64,
	_ string,
) (*ConsoleDPoPSession, error) {
	// 1. 获取解密后的会话材料
	material, err := p.sessionService.GetSessionMaterial(ctx, accountID, proxyID)
	if err != nil {
		return nil, fmt.Errorf("get session material: %w", err)
	}
	if material.Source != "console" {
		return nil, fmt.Errorf("session source is not console, got: %s", material.Source)
	}

	// 2. 生成 P-256 密钥对（仅内存，永不落库）
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate P-256 key: %w", err)
	}

	// 3. 构造 JWK（P-256 坐标需补齐 32 字节）
	xBytes := pad32(privateKey.X.Bytes())
	yBytes := pad32(privateKey.Y.Bytes())
	publicJWK := map[string]string{
		"kty": "EC",
		"crv": "P-256",
		"x":   base64.RawURLEncoding.EncodeToString(xBytes),
		"y":   base64.RawURLEncoding.EncodeToString(yBytes),
	}

	// 4. 构建 DPoP proof 用于 token 换票请求
	dpopTokenURL := "https://console.x.ai/v1/dpop/token"
	dpopProof, err := buildDPoPProof(privateKey, publicJWK, "POST", dpopTokenURL, "")
	if err != nil {
		return nil, fmt.Errorf("build DPoP proof: %w", err)
	}

	// 5. 请求 DPoP token（通过 httpUpstream 支持代理出口）
	body := map[string]any{"jwk": publicJWK}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", dpopTokenURL, strings.NewReader(string(bodyJSON)))
	if err != nil {
		return nil, fmt.Errorf("create DPoP request: %w", err)
	}
	req.Header.Set("Cookie", "sso="+material.SSO+"; sso-rw="+material.SSORw)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("DPoP", dpopProof)
	req.Header.Set("User-Agent", material.BrowserUA)
	req.Header.Set("Origin", "https://console.x.ai")
	req.Header.Set("Referer", "https://console.x.ai/")

	proxyURL := p.resolveProxyURL(proxyID)
	resp, err := p.httpUpstream.Do(req, proxyURL, accountID, 1)
	if err != nil {
		return nil, fmt.Errorf("DPoP token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("DPoP token exchange failed (HTTP %d): %s", resp.StatusCode, respBody)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("parse DPoP response: %w", err)
	}

	expiresIn := tokenResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 300 // 默认 5 分钟
	}

	session := &ConsoleDPoPSession{
		AccessToken: tokenResp.AccessToken,
		PrivateKey:  privateKey,
		PublicJWK:   publicJWK,
		ExpiresAt:   time.Now().Add(time.Duration(expiresIn) * time.Second),
	}

	p.mu.Lock()
	p.sessions[fmt.Sprintf("console:%d:%v", accountID, proxyID)] = session
	p.mu.Unlock()

	return session, nil
}

// BuildDPoPProof 为指定资源请求构建 DPoP proof
func (p *GrokConsoleDPoPProvider) BuildDPoPProof(
	session *ConsoleDPoPSession,
	method, uri string,
) (string, error) {
	ath := sha256Of(session.AccessToken)
	return buildDPoPProof(session.PrivateKey, session.PublicJWK, method, uri, ath)
}

// InvalidateSession 失效账号的所有 DPoP 会话
func (p *GrokConsoleDPoPProvider) InvalidateSession(accountID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key := range p.sessions {
		delete(p.sessions, key)
	}
}

// BuildConsoleResponsesRequest 构建完整的 Console API 请求（SSO Cookie + DPoP Authorization + 浏览器头）
// 参考项目 grok2api: applyBrowserHeaders + applyDPoPAuthorization
func (p *GrokConsoleDPoPProvider) BuildConsoleResponsesRequest(
	ctx context.Context,
	accountID int64,
	proxyID *int64,
	body []byte,
) (*http.Request, error) {
	material, err := p.sessionService.GetSessionMaterial(ctx, accountID, proxyID)
	if err != nil {
		return nil, fmt.Errorf("get session material: %w", err)
	}

	session, err := p.GetOrCreateSession(ctx, accountID, proxyID)
	if err != nil {
		return nil, fmt.Errorf("get DPoP session: %w", err)
	}

	endpoint := "https://console.x.ai/v1/responses"
	proof, err := p.BuildDPoPProof(session, "POST", endpoint)
	if err != nil {
		return nil, fmt.Errorf("build DPoP proof: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, nil)
	if err != nil {
		return nil, err
	}

	// SSO Cookie (applyBrowserHeaders)
	req.Header.Set("Cookie", "sso="+material.SSO+"; sso-rw="+material.SSORw)

	// DPoP Authorization (applyDPoPAuthorization)
	req.Header.Set("Authorization", "DPoP "+session.AccessToken)
	req.Header.Set("DPoP", proof)

	// Full browser headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Origin", "https://console.x.ai")
	req.Header.Set("Referer", "https://console.x.ai/")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("User-Agent", material.BrowserUA)
	req.Header.Set("x-cluster", "https://us-east-1.api.x.ai")

	if len(body) > 0 {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}

	return req, nil
}

// buildDPoPProof 用 golang-jwt 构建 ES256 签名的 DPoP proof JWT
func buildDPoPProof(key *ecdsa.PrivateKey, jwk map[string]string, method, uri, ath string) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"jti": fmt.Sprintf("%d", now.UnixNano()),
		"htm": method,
		"htu": uri,
		"iat": now.Unix(),
	}
	if ath != "" {
		claims["ath"] = ath
	}

	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["typ"] = "dpop+jwt"
	token.Header["jwk"] = jwk

	signed, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign DPoP proof: %w", err)
	}
	return signed, nil
}

func pad32(input []byte) []byte {
	if len(input) >= 32 {
		return input
	}
	padded := make([]byte, 32)
	copy(padded[32-len(input):], input)
	return padded
}

func sha256Of(input string) string {
	hash := sha256.Sum256([]byte(input))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

// Ensure interfaces are implemented
var _ = url.Values{}

// SetProxyURLResolver 设置代理 URL 解析函数，由调用方（网关/测试服务）注入。
// 签名：func(proxyID int64) (string, error)
func (p *GrokConsoleDPoPProvider) SetProxyURLResolver(resolver func(int64) string) {
	p.proxyURLResolver = resolver
}

// resolveProxyURL 解析代理 URL；无代理或无解析器时返回空字符串（直连）。
func (p *GrokConsoleDPoPProvider) resolveProxyURL(proxyID *int64) string {
	if proxyID == nil || p.proxyURLResolver == nil {
		return ""
	}
	return p.proxyURLResolver(*proxyID)
}

// ConsoleUsageQuota 是 /v1/usage 返回的单类配额。
type ConsoleUsageQuota struct {
	Kind      string `json:"kind"`
	Limit     int64  `json:"limit"`
	Used      int64  `json:"used"`
	Remaining int64  `json:"remaining"`
}

// ConsoleUsageSnapshot 是 Console 账号的真实配额快照。
type ConsoleUsageSnapshot struct {
	Quotas    []ConsoleUsageQuota `json:"quotas"`
	FetchedAt time.Time           `json:"fetched_at"`
}

// GetConsoleUsageQuota 按类别返回配额（chat/image/video）；缺失时返回 nil。
func (s *ConsoleUsageSnapshot) GetConsoleUsageQuota(kind string) *ConsoleUsageQuota {
	if s == nil {
		return nil
	}
	for i := range s.Quotas {
		if strings.EqualFold(s.Quotas[i].Kind, kind) {
			return &s.Quotas[i]
		}
	}
	return nil
}

// FetchConsoleUsage 通过 SSO cookie 直连 console.x.ai/v1/usage 获取真实配额。
// 该端点只读、不消耗推理配额；401/403 表示会话失效。
func (p *GrokConsoleDPoPProvider) FetchConsoleUsage(
	ctx context.Context,
	accountID int64,
	proxyID *int64,
) (*ConsoleUsageSnapshot, error) {
	material, err := p.sessionService.GetSessionMaterial(ctx, accountID, proxyID)
	if err != nil {
		return nil, fmt.Errorf("get session material: %w", err)
	}

	usageURL := "https://console.x.ai/v1/usage"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", "sso="+material.SSO+"; sso-rw="+material.SSORw)
	req.Header.Set("User-Agent", material.BrowserUA)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://console.x.ai")
	req.Header.Set("Referer", "https://console.x.ai/")

	proxyURL := p.resolveProxyURL(proxyID)
	resp, err := p.httpUpstream.Do(req, proxyURL, accountID, 1)
	if err != nil {
		return nil, fmt.Errorf("console usage request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("read console usage: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		_ = p.sessionService.MarkReauthRequired(ctx, accountID, "console_usage_unauthorized")
		return nil, fmt.Errorf("console usage unauthorized (%d)", resp.StatusCode)
	case http.StatusOK:
		// fallthrough to decode
	default:
		return nil, fmt.Errorf("console usage returned %d: %s", resp.StatusCode, truncateConsoleBody(body))
	}

	var parsed struct {
		Quotas []ConsoleUsageQuota `json:"quotas"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode console usage: %w", err)
	}
	return &ConsoleUsageSnapshot{Quotas: parsed.Quotas, FetchedAt: time.Now().UTC()}, nil
}

func truncateConsoleBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
