package service

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const (
	GrokOAuthIssuer        = "https://auth.x.ai"
	GrokOAuthAuthorizeURL  = "https://auth.x.ai/oauth2/authorize"
	GrokOAuthDiscoveryURL  = "https://auth.x.ai/.well-known/openid-configuration"
	GrokOAuthTokenEndpoint = "https://auth.x.ai/oauth2/token"
	GrokOAuthAPIBaseURL    = "https://api.x.ai/v1"
	GrokOAuthRedirectURI   = "http://127.0.0.1:56121/callback"
	GrokOAuthClientID      = "b1a00492-073a-47ea-816f-4c329264a828"
	GrokOAuthScope         = "openid profile email offline_access grok-cli:access api:access"
)

type GrokOAuthService struct {
	sessionStore  *openai.SessionStore
	proxyRepo     ProxyRepository
	httpClient    *http.Client
	tokenEndpoint string
}

func NewGrokOAuthService(proxyRepo ProxyRepository) *GrokOAuthService {
	return &GrokOAuthService{
		sessionStore:  openai.NewSessionStore(),
		proxyRepo:     proxyRepo,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		tokenEndpoint: GrokOAuthTokenEndpoint,
	}
}

type GrokAuthURLResult struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`
}

type GrokExchangeCodeInput struct {
	SessionID   string
	Code        string
	State       string
	RedirectURI string
	ProxyID     *int64
}

type GrokTokenInfo struct {
	AccessToken   string   `json:"access_token"`
	RefreshToken  string   `json:"refresh_token,omitempty"`
	IDToken       string   `json:"id_token,omitempty"`
	TokenType     string   `json:"token_type,omitempty"`
	ClientID      string   `json:"client_id,omitempty"`
	TokenEndpoint string   `json:"token_endpoint,omitempty"`
	APIBaseURL    string   `json:"api_base_url,omitempty"`
	Scope         string   `json:"scope,omitempty"`
	Scopes        []string `json:"scopes,omitempty"`
	ExpiresIn     int64    `json:"expires_in,omitempty"`
	ExpiresAt     int64    `json:"expires_at,omitempty"`
	Email         string   `json:"email,omitempty"`
	Subject       string   `json:"sub,omitempty"`
	Name          string   `json:"name,omitempty"`
}

type grokTokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	IDToken          string `json:"id_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int64  `json:"expires_in"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (s *GrokOAuthService) GenerateAuthURL(ctx context.Context, proxyID *int64, redirectURI string) (*GrokAuthURLResult, error) {
	state, err := openai.GenerateState()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "GROK_OAUTH_STATE_FAILED", "failed to generate state: %v", err)
	}
	codeVerifier, err := openai.GenerateCodeVerifier()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "GROK_OAUTH_VERIFIER_FAILED", "failed to generate code verifier: %v", err)
	}
	codeChallenge := openai.GenerateCodeChallenge(codeVerifier)
	sessionID, err := openai.GenerateSessionID()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "GROK_OAUTH_SESSION_FAILED", "failed to generate session ID: %v", err)
	}

	var proxyURL string
	if proxyID != nil {
		proxy, err := s.proxyRepo.GetByID(ctx, *proxyID)
		if err != nil {
			return nil, infraerrors.Newf(http.StatusBadRequest, "GROK_OAUTH_PROXY_NOT_FOUND", "proxy not found: %v", err)
		}
		if proxy != nil {
			proxyURL = proxy.URL()
		}
	}

	if strings.TrimSpace(redirectURI) == "" {
		redirectURI = GrokOAuthRedirectURI
	}

	s.sessionStore.Set(sessionID, &openai.OAuthSession{
		State:        state,
		CodeVerifier: codeVerifier,
		ClientID:     GrokOAuthClientID,
		RedirectURI:  redirectURI,
		ProxyURL:     proxyURL,
		CreatedAt:    time.Now(),
	})

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", GrokOAuthClientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", GrokOAuthScope)
	params.Set("state", state)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	params.Set("nonce", state)
	params.Set("plan", "generic")
	params.Set("referrer", "sub2api")

	return &GrokAuthURLResult{
		AuthURL:   GrokOAuthAuthorizeURL + "?" + params.Encode(),
		SessionID: sessionID,
		State:     state,
	}, nil
}

func (s *GrokOAuthService) ExchangeCode(ctx context.Context, input *GrokExchangeCodeInput) (*GrokTokenInfo, error) {
	session, ok := s.sessionStore.Get(strings.TrimSpace(input.SessionID))
	if !ok {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_SESSION_NOT_FOUND", "session not found or expired")
	}
	if strings.TrimSpace(input.State) == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_STATE_REQUIRED", "oauth state is required")
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(input.State)), []byte(session.State)) != 1 {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_INVALID_STATE", "invalid oauth state")
	}

	proxyURL := session.ProxyURL
	if input.ProxyID != nil {
		proxy, err := s.proxyRepo.GetByID(ctx, *input.ProxyID)
		if err != nil {
			return nil, infraerrors.Newf(http.StatusBadRequest, "GROK_OAUTH_PROXY_NOT_FOUND", "proxy not found: %v", err)
		}
		if proxy != nil {
			proxyURL = proxy.URL()
		}
	}

	redirectURI := strings.TrimSpace(session.RedirectURI)
	if strings.TrimSpace(input.RedirectURI) != "" {
		redirectURI = strings.TrimSpace(input.RedirectURI)
	}

	resp, err := s.requestToken(ctx, proxyURL, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {strings.TrimSpace(input.Code)},
		"redirect_uri":  {redirectURI},
		"client_id":     {GrokOAuthClientID},
		"code_verifier": {session.CodeVerifier},
	})
	if err != nil {
		return nil, err
	}

	s.sessionStore.Delete(strings.TrimSpace(input.SessionID))
	return s.buildTokenInfo(resp, ""), nil
}

func (s *GrokOAuthService) RefreshToken(ctx context.Context, refreshToken string, proxyURL string) (*GrokTokenInfo, error) {
	resp, err := s.requestToken(ctx, proxyURL, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {GrokOAuthClientID},
		"refresh_token": {strings.TrimSpace(refreshToken)},
	})
	if err != nil {
		return nil, err
	}
	return s.buildTokenInfo(resp, strings.TrimSpace(refreshToken)), nil
}

func (s *GrokOAuthService) RefreshAccountToken(ctx context.Context, account *Account) (*GrokTokenInfo, error) {
	if account == nil {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_ACCOUNT_REQUIRED", "account is required")
	}
	if account.Platform != PlatformGrok {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_INVALID_ACCOUNT", "account is not a Grok account")
	}
	if account.Type != AccountTypeOAuth {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_INVALID_ACCOUNT_TYPE", "account is not an OAuth account")
	}
	refreshToken := strings.TrimSpace(account.GetCredential("refresh_token"))
	if refreshToken == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_NO_REFRESH_TOKEN", "no refresh token available")
	}

	var proxyURL string
	if account.ProxyID != nil {
		proxy, err := s.proxyRepo.GetByID(ctx, *account.ProxyID)
		if err == nil && proxy != nil {
			proxyURL = proxy.URL()
		}
	}

	info, err := s.RefreshToken(ctx, refreshToken, proxyURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(info.RefreshToken) == "" {
		info.RefreshToken = refreshToken
	}
	return info, nil
}

func (s *GrokOAuthService) BuildAccountCredentials(tokenInfo *GrokTokenInfo) map[string]any {
	creds := map[string]any{
		"access_token":   tokenInfo.AccessToken,
		"client_id":      firstNonEmptyGrok(strings.TrimSpace(tokenInfo.ClientID), GrokOAuthClientID),
		"token_endpoint": firstNonEmptyGrok(strings.TrimSpace(tokenInfo.TokenEndpoint), GrokOAuthTokenEndpoint),
		"api_base_url":   firstNonEmptyGrok(strings.TrimSpace(tokenInfo.APIBaseURL), GrokOAuthAPIBaseURL),
	}
	if tokenInfo.ExpiresAt > 0 {
		creds["expires_at"] = tokenInfo.ExpiresAt
	}
	if strings.TrimSpace(tokenInfo.RefreshToken) != "" {
		creds["refresh_token"] = strings.TrimSpace(tokenInfo.RefreshToken)
	}
	if strings.TrimSpace(tokenInfo.IDToken) != "" {
		creds["id_token"] = strings.TrimSpace(tokenInfo.IDToken)
	}
	if strings.TrimSpace(tokenInfo.TokenType) != "" {
		creds["token_type"] = strings.TrimSpace(tokenInfo.TokenType)
	}
	if strings.TrimSpace(tokenInfo.Scope) != "" {
		creds["scope"] = strings.TrimSpace(tokenInfo.Scope)
	}
	if len(tokenInfo.Scopes) > 0 {
		creds["scopes"] = append([]string(nil), tokenInfo.Scopes...)
	}
	return creds
}

func (s *GrokOAuthService) Stop() {
	s.sessionStore.Stop()
}

func (s *GrokOAuthService) requestToken(ctx context.Context, proxyURL string, form url.Values) (*grokTokenResponse, error) {
	if strings.TrimSpace(form.Get("client_id")) == "" {
		form.Set("client_id", GrokOAuthClientID)
	}
	client, err := s.httpClientForProxy(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadRequest, "GROK_OAUTH_PROXY_INVALID", "invalid proxy configuration: %v", err)
	}
	tokenEndpoint := firstNonEmptyGrok(strings.TrimSpace(s.tokenEndpoint), GrokOAuthTokenEndpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "GROK_OAUTH_REQUEST_BUILD_FAILED", "failed to build token request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_REQUEST_FAILED", "token request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	var tokenResp grokTokenResponse
	_ = json.Unmarshal(body, &tokenResp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || strings.TrimSpace(tokenResp.AccessToken) == "" {
		if tokenResp.Error != "" {
			msg := tokenResp.Error
			if strings.TrimSpace(tokenResp.ErrorDescription) != "" {
				msg = fmt.Sprintf("%s: %s", tokenResp.Error, strings.TrimSpace(tokenResp.ErrorDescription))
			}
			return nil, infraerrors.New(http.StatusBadGateway, "GROK_OAUTH_TOKEN_FAILED", msg)
		}
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_TOKEN_FAILED", "token request failed with status %d", resp.StatusCode)
	}
	return &tokenResp, nil
}

func (s *GrokOAuthService) buildTokenInfo(resp *grokTokenResponse, fallbackRefreshToken string) *GrokTokenInfo {
	expiresIn := resp.ExpiresIn
	if expiresIn < 0 {
		expiresIn = 0
	}
	info := &GrokTokenInfo{
		AccessToken:   strings.TrimSpace(resp.AccessToken),
		RefreshToken:  strings.TrimSpace(resp.RefreshToken),
		IDToken:       strings.TrimSpace(resp.IDToken),
		TokenType:     firstNonEmptyGrok(strings.TrimSpace(resp.TokenType), "Bearer"),
		ClientID:      GrokOAuthClientID,
		TokenEndpoint: GrokOAuthTokenEndpoint,
		APIBaseURL:    GrokOAuthAPIBaseURL,
		Scope:         normalizeScope(resp.Scope),
		Scopes:        splitScopes(resp.Scope),
		ExpiresIn:     expiresIn,
		ExpiresAt:     time.Now().Unix() + expiresIn,
	}
	if info.RefreshToken == "" {
		info.RefreshToken = strings.TrimSpace(fallbackRefreshToken)
	}
	if claims := parseUnverifiedJWTClaims(info.IDToken); claims != nil {
		info.Email = readStringClaim(claims, "email")
		info.Subject = readStringClaim(claims, "sub")
		info.Name = readStringClaim(claims, "name")
	}
	return info
}

func (s *GrokOAuthService) httpClientForProxy(proxyURL string) (*http.Client, error) {
	trimmed := strings.TrimSpace(proxyURL)
	if trimmed == "" {
		return s.httpClient, nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(parsed)
	return &http.Client{Timeout: 30 * time.Second, Transport: transport}, nil
}

func parseUnverifiedJWTClaims(raw string) map[string]any {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	return claims
}

func readStringClaim(claims map[string]any, key string) string {
	if claims == nil {
		return ""
	}
	value, ok := claims[key]
	if !ok || value == nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func normalizeScope(scope string) string {
	return strings.Join(splitScopes(scope), " ")
}

func splitScopes(scope string) []string {
	fields := strings.Fields(strings.ReplaceAll(strings.TrimSpace(scope), ",", " "))
	if len(fields) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(fields))
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		result = append(result, field)
	}
	return result
}

func firstNonEmptyGrok(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
