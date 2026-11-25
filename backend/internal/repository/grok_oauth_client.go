package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	sharedhttp "github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/imroc/req/v3"
)

type grokOAuthClient struct {
	tokenURL       string
	egressResolver EgressProxyResolver
}

const (
	accountsBaseURL      = "https://accounts.x.ai"
	loginRPCEndpoint     = accountsBaseURL + "/api/rpc"
	turnstileWebsiteURL  = accountsBaseURL
	turnstileWebsiteKey  = "0x4AAAAAAAhr9JGVDZbrZOo0"
	yesCaptchaCreateTask = "https://api.yescaptcha.com/createTask"
	yesCaptchaGetResult  = "https://api.yescaptcha.com/getTaskResult"
)

func NewGrokOAuthClient(resolver EgressProxyResolver) service.GrokOAuthClient {
	tokenURL, err := xai.ValidatedTokenURL()
	if err != nil || strings.TrimSpace(tokenURL) == "" {
		tokenURL = xai.DefaultTokenURL
	}
	return &grokOAuthClient{tokenURL: tokenURL, egressResolver: resolver}
}

func (c *grokOAuthClient) resolveEgress(ctx context.Context, proxyURL string) string {
	if c == nil || c.egressResolver == nil || strings.TrimSpace(proxyURL) == "" {
		return ""
	}
	normalized, _, err := proxyurl.Parse(proxyURL)
	if err != nil {
		return ""
	}
	return c.egressResolver.ResolveEgress(ctx, normalized)
}

func (c *grokOAuthClient) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI, proxyURL, clientID string) (*xai.TokenResponse, error) {
	client, err := createGrokReqClient(proxyURL, c.resolveEgress(ctx, proxyURL))
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}

	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = xai.EffectiveClientID()
	}

	formData := url.Values{}
	formData.Set("grant_type", "authorization_code")
	formData.Set("client_id", clientID)
	formData.Set("code", code)
	formData.Set("redirect_uri", xai.EffectiveRedirectURI(redirectURI))
	formData.Set("code_verifier", codeVerifier)

	var tokenResp xai.TokenResponse
	resp, err := client.R().
		SetContext(ctx).
		SetHeader("User-Agent", "sub2api-grok-oauth/1.0").
		SetFormDataFromValues(formData).
		SetSuccessResult(&tokenResp).
		Post(c.tokenURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_REQUEST_FAILED", "request failed: %v", err)
	}
	if !resp.IsSuccessState() {
		return nil, grokOAuthStatusError("GROK_OAUTH_TOKEN_EXCHANGE_FAILED", "token exchange failed", resp)
	}
	return &tokenResp, nil
}

func (c *grokOAuthClient) RefreshToken(ctx context.Context, refreshToken, proxyURL, clientID string) (*xai.TokenResponse, error) {
	client, err := createGrokReqClient(proxyURL, c.resolveEgress(ctx, proxyURL))
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}

	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = xai.EffectiveClientID()
	}

	formData := url.Values{}
	formData.Set("grant_type", "refresh_token")
	formData.Set("client_id", clientID)
	formData.Set("refresh_token", refreshToken)

	var tokenResp xai.TokenResponse
	resp, err := client.R().
		SetContext(ctx).
		SetHeader("User-Agent", "sub2api-grok-oauth/1.0").
		SetFormDataFromValues(formData).
		SetSuccessResult(&tokenResp).
		Post(c.tokenURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_REQUEST_FAILED", "request failed: %v", err)
	}
	if !resp.IsSuccessState() {
		return nil, grokOAuthStatusError("GROK_OAUTH_TOKEN_REFRESH_FAILED", "token refresh failed", resp)
	}
	return &tokenResp, nil
}

// LoginWithPassword authenticates against accounts.x.ai and returns an ephemeral SSO cookie.
// Password and SSO must never be written to account credentials or logs.
func (c *grokOAuthClient) LoginWithPassword(ctx context.Context, email, password, proxyURL string) (*service.GrokPasswordLoginResult, error) {
	turnstileToken, err := solveTurnstile(ctx)
	if err != nil {
		return nil, err
	}
	httpClient, err := createGrokHTTPClient(proxyURL, true)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}
	cookieSetterURL, err := createGrokPasswordSession(ctx, httpClient, strings.TrimSpace(email), password, turnstileToken)
	if err != nil {
		return nil, err
	}
	ssoToken, err := extractGrokSSOToken(ctx, httpClient, cookieSetterURL)
	if err != nil {
		return nil, err
	}
	return &service.GrokPasswordLoginResult{
		Email:    strings.TrimSpace(email),
		SSOToken: ssoToken,
	}, nil
}

func (c *grokOAuthClient) ConvertSSOToBuild(ctx context.Context, ssoToken, proxyURL string) (*xai.TokenResponse, error) {
	client, err := createGrokSSOHTTPClient(proxyURL, c.resolveEgress(ctx, proxyURL))
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_SSO_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}

	requestCtx, cancel := context.WithTimeout(ctx, xai.SSOConversionTimeout)
	defer cancel()
	tokenResp, err := xai.ConvertSSOToBuild(requestCtx, ssoToken, &xai.SSODeviceOptions{HTTPClient: client})
	if err != nil {
		return nil, grokSSOConversionError(err)
	}
	return tokenResp, nil
}

func createGrokReqClient(proxyURL, egressURL string) (*req.Client, error) {
	if strings.TrimSpace(egressURL) != "" {
		transport, err := buildOAuthEgressTransport(proxyURL, egressURL)
		if err != nil {
			return nil, err
		}
		client := req.C().SetTimeout(60 * time.Second)
		client.GetClient().Transport = transport
		// Skip instrumentReqClient for the egress path: the custom transport
		// already handles servertiming and the req Transport wrapper would
		// override our DialContext/Proxy configuration.
		return client, nil
	}
	return getSharedReqClient(reqClientOptions{
		ProxyURL: proxyURL,
		Timeout:  60 * time.Second,
	})
}

func createGrokSSOHTTPClient(proxyURL, egressURL string) (*http.Client, error) {
	if strings.TrimSpace(egressURL) != "" {
		transport, err := buildOAuthEgressTransport(proxyURL, egressURL)
		if err != nil {
			return nil, err
		}
		transport.DisableCompression = false
		client := &http.Client{
			Transport: transport,
			Timeout:   xai.SSOConversionTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		return client, nil
	}
	client, err := sharedhttp.GetClient(sharedhttp.Options{
		ProxyURL:              proxyURL,
		Timeout:               xai.SSOConversionTimeout,
		ResponseHeaderTimeout: 30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone, nil
}

func buildOAuthEgressTransport(proxyURL, egressURL string) (*http.Transport, error) {
	base := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	_, parsedTarget, err := proxyurl.Parse(proxyURL)
	if err != nil || parsedTarget == nil {
		return nil, fmt.Errorf("parse target proxy: %w", err)
	}
	_, parsedEgress, err := proxyurl.Parse(egressURL)
	if err != nil || parsedEgress == nil {
		return nil, fmt.Errorf("parse egress proxy: %w", err)
	}
	egressDialer, err := newEgressDialer(parsedEgress.String(), &net.Dialer{Timeout: 10 * time.Second})
	if err != nil {
		return nil, err
	}
	base.DialContext = egressDialer.DialContext
	if err := proxyutil.ConfigureTransportProxyWithDialContextToProxy(base, parsedTarget, net.JoinHostPort(parsedTarget.Hostname(), parsedTarget.Port()), egressDialer.DialContext); err != nil {
		return nil, err
	}
	return base, nil
}

func grokSSOConversionError(err error) error {
	if errors.Is(err, xai.ErrSSOUnauthorized) {
		return infraerrors.New(http.StatusUnauthorized, "GROK_SSO_UNAUTHORIZED", "Grok Web SSO cookie is invalid or expired")
	}
	if errors.Is(err, xai.ErrSSOAuthorizationDenied) {
		return infraerrors.New(http.StatusForbidden, "GROK_SSO_AUTHORIZATION_DENIED", "xAI device authorization was denied or expired")
	}
	var statusErr xai.SSOHTTPError
	if errors.As(err, &statusErr) {
		statusCode := http.StatusBadGateway
		if statusErr.Status == http.StatusForbidden {
			statusCode = http.StatusForbidden
		}
		return infraerrors.Newf(statusCode, "GROK_SSO_UPSTREAM_FAILED", "xAI SSO conversion failed: %v", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return infraerrors.Newf(http.StatusGatewayTimeout, "GROK_SSO_TIMEOUT", "xAI SSO conversion timed out: %v", err)
	}
	return infraerrors.Newf(http.StatusBadGateway, "GROK_SSO_CONVERSION_FAILED", "xAI SSO conversion failed: %v", err)
}

func grokOAuthStatusError(code, message string, resp *req.Response) error {
	statusCode := http.StatusBadGateway
	errorCode := code
	upstreamStatus := 0
	body := ""
	if resp != nil {
		upstreamStatus = resp.StatusCode
		body = logredact.RedactText(resp.String())
		if resp.StatusCode == http.StatusForbidden && grokOAuthHasExplicitEntitlementDenial(body) {
			statusCode = http.StatusForbidden
			errorCode = "GROK_OAUTH_ENTITLEMENT_DENIED"
		}
	}
	return infraerrors.Newf(statusCode, errorCode, "%s: status %d, body: %s", message, upstreamStatus, body)
}

func grokOAuthHasExplicitEntitlementDenial(body string) bool {
	lower := strings.ToLower(body)
	// Billing exhaustion is recoverable. xAI may include a generic
	// access_denied code alongside the quota message, so it must win over the
	// entitlement marker during token refresh.
	for _, phrase := range []string{
		"spending limit",
		"run out of credits",
		"out of credits",
		"credits exhausted",
		"included free usage",
	} {
		if strings.Contains(lower, phrase) {
			return false
		}
	}
	compact := strings.NewReplacer(" ", "", "\n", "", "\r", "", "\t", "").Replace(lower)
	for _, field := range []string{"error", "code", "reason"} {
		for _, value := range []string{"access_denied", "entitlement_denied", "subscription_required", "no_active_subscription"} {
			if strings.Contains(compact, `"`+field+`":"`+value+`"`) {
				return true
			}
		}
	}
	return strings.Contains(lower, "entitlement denied") ||
		strings.Contains(lower, "subscription required") ||
		strings.Contains(lower, "no active grok subscription")
}

func createGrokHTTPClient(proxyURL string, noRedirect bool) (*http.Client, error) {
	transport := &http.Transport{}
	if strings.TrimSpace(proxyURL) != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	client := &http.Client{Timeout: 120 * time.Second, Transport: transport}
	if noRedirect {
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return client, nil
}

func solveTurnstile(ctx context.Context) (string, error) {
	clientKey := strings.TrimSpace(os.Getenv("YESCAPTCHA_CLIENT_KEY"))
	if clientKey == "" {
		clientKey = strings.TrimSpace(os.Getenv("YESCAPTCHA_API_KEY"))
	}
	if clientKey == "" {
		return "", infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_CAPTCHA_KEY_REQUIRED", "yescaptcha client key is required for Grok password authorization")
	}
	createBody, err := json.Marshal(map[string]any{
		"clientKey": clientKey,
		"task": map[string]any{
			"type":       "TurnstileTaskProxyless",
			"websiteURL": turnstileWebsiteURL,
			"websiteKey": turnstileWebsiteKey,
		},
	})
	if err != nil {
		return "", infraerrors.Newf(http.StatusInternalServerError, "GROK_OAUTH_CAPTCHA_FAILED", "encode captcha create request failed: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, yesCaptchaCreateTask, bytes.NewReader(createBody))
	if err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CAPTCHA_FAILED", "build captcha create request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CAPTCHA_FAILED", "create captcha task failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var createResp struct {
		ErrorID          int    `json:"errorId"`
		TaskID           string `json:"taskId"`
		ErrorDescription string `json:"errorDescription"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CAPTCHA_FAILED", "decode captcha create response failed: %v", err)
	}
	if createResp.ErrorID != 0 || strings.TrimSpace(createResp.TaskID) == "" {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CAPTCHA_FAILED", "captcha create failed: %s", createResp.ErrorDescription)
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
		body, err := json.Marshal(map[string]any{"clientKey": clientKey, "taskId": createResp.TaskID})
		if err != nil {
			return "", infraerrors.Newf(http.StatusInternalServerError, "GROK_OAUTH_CAPTCHA_FAILED", "encode captcha poll request failed: %v", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, yesCaptchaGetResult, bytes.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		var pollResp struct {
			ErrorID          int    `json:"errorId"`
			Status           string `json:"status"`
			ErrorDescription string `json:"errorDescription"`
			Solution         struct {
				Token string `json:"token"`
			} `json:"solution"`
		}
		err = json.NewDecoder(resp.Body).Decode(&pollResp)
		_ = resp.Body.Close()
		if err != nil {
			continue
		}
		if pollResp.ErrorID != 0 {
			return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CAPTCHA_FAILED", "captcha poll failed: %s", pollResp.ErrorDescription)
		}
		if pollResp.Status == "ready" && strings.TrimSpace(pollResp.Solution.Token) != "" {
			return pollResp.Solution.Token, nil
		}
	}
	return "", infraerrors.New(http.StatusGatewayTimeout, "GROK_OAUTH_CAPTCHA_TIMEOUT", "captcha solve timed out")
}

func createGrokPasswordSession(ctx context.Context, client *http.Client, email, password, turnstileToken string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"rpc": "createSession",
		"req": map[string]any{
			"createSessionRequest": map[string]any{
				"credentials": map[string]any{
					"case": "emailAndPassword",
					"value": map[string]any{
						"email":             email,
						"clearTextPassword": password,
					},
				},
			},
			"turnstileToken": turnstileToken,
		},
	})
	if err != nil {
		return "", infraerrors.Newf(http.StatusInternalServerError, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "encode password login request failed: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginRPCEndpoint, bytes.NewReader(payload))
	if err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "build password login request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", accountsBaseURL)
	req.Header.Set("Referer", accountsBaseURL+"/sign-in?redirect=grok-com&email=true")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "*/*")
	resp, err := client.Do(req)
	if err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "password login request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "password login returned status %d: %s", resp.StatusCode, logredact.RedactText(string(body)))
	}
	var loginResp struct {
		CookieSetterURL string `json:"cookieSetterUrl"`
		Error           string `json:"error"`
	}
	if err := json.Unmarshal(body, &loginResp); err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "decode password login response failed: %v", err)
	}
	if strings.TrimSpace(loginResp.Error) != "" {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "password login error: %s", logredact.RedactText(loginResp.Error))
	}
	if strings.TrimSpace(loginResp.CookieSetterURL) == "" {
		return "", infraerrors.New(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "password login did not return cookieSetterUrl")
	}
	return loginResp.CookieSetterURL, nil
}

func extractGrokSSOToken(ctx context.Context, client *http.Client, cookieSetterURL string) (string, error) {
	safeURL, err := validateGrokCookieSetterURL(cookieSetterURL)
	if err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "invalid cookie setter url: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, safeURL.String(), nil)
	if err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "build cookie setter request: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Referer", accountsBaseURL+"/")
	resp, err := client.Do(req)
	if err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "follow cookie setter url failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	for _, cookie := range resp.Header.Values("Set-Cookie") {
		if token, ok := strings.CutPrefix(cookie, "sso="); ok {
			if idx := strings.Index(token, ";"); idx > 0 {
				token = token[:idx]
			}
			return strings.TrimSpace(token), nil
		}
	}
	return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "no sso cookie found in response (status=%d)", resp.StatusCode)
}

func validateGrokCookieSetterURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "accounts.x.ai") {
		return nil, fmt.Errorf("url must use https://accounts.x.ai")
	}
	if parsed.User != nil || parsed.Port() != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return nil, fmt.Errorf("url contains disallowed authority or fragment components")
	}
	return parsed, nil
}
