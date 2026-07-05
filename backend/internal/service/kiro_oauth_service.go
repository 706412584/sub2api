package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type KiroTokenInfo struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
	ExpiresAt    int64
	ProfileArn   string
	TokenType    string
}

type KiroTokenRefresher struct{}

func NewKiroTokenRefresher() *KiroTokenRefresher {
	return &KiroTokenRefresher{}
}

func (r *KiroTokenRefresher) CacheKey(account *Account) string {
	return fmt.Sprintf("kiro:%d", account.ID)
}

func (r *KiroTokenRefresher) CanRefresh(account *Account) bool {
	return account.Platform == PlatformKiro && account.Type == AccountTypeOAuth
}

func (r *KiroTokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	if !r.CanRefresh(account) {
		return false
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil {
		return true
	}
	return time.Until(*expiresAt) < refreshWindow
}

func (r *KiroTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	tokenInfo, err := RefreshKiroAccountToken(ctx, account)
	if err != nil {
		return nil, err
	}
	return MergeCredentials(account.Credentials, BuildKiroAccountCredentials(tokenInfo)), nil
}

func RefreshKiroAccountToken(ctx context.Context, account *Account) (*KiroTokenInfo, error) {
	if account == nil {
		return nil, fmt.Errorf("account is nil")
	}
	refreshToken := strings.TrimSpace(account.GetCredential("refresh_token"))
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh_token is required")
	}
	authMethod := normalizeKiroRefreshAuthMethod(account.GetCredential("auth_method"))
	if authMethod == "" {
		authMethod = inferKiroRefreshAuthMethod(account)
	}
	client := &http.Client{Timeout: 30 * time.Second}

	switch authMethod {
	case "external_idp":
		return refreshKiroExternalIDPToken(ctx, client, account, refreshToken)
	case "social":
		return refreshKiroSocialToken(ctx, client, refreshToken)
	default:
		return refreshKiroOIDCToken(ctx, client, account, refreshToken)
	}
}

func BuildKiroAccountCredentials(tokenInfo *KiroTokenInfo) map[string]any {
	credentials := map[string]any{
		"access_token": tokenInfo.AccessToken,
		"token_type":   tokenInfo.TokenType,
		"expires_in":   tokenInfo.ExpiresIn,
		"expires_at":   tokenInfo.ExpiresAt,
	}
	if strings.TrimSpace(tokenInfo.RefreshToken) != "" {
		credentials["refresh_token"] = tokenInfo.RefreshToken
	}
	if strings.TrimSpace(tokenInfo.ProfileArn) != "" {
		credentials["profile_arn"] = tokenInfo.ProfileArn
	}
	return credentials
}

func normalizeKiroRefreshAuthMethod(method string) string {
	lower := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(method), "-", "_"))
	switch lower {
	case "idc", "builderid", "builder_id":
		return "idc"
	case "social", "google", "github":
		return "social"
	case "external_idp", "externalidp", "external", "enterprise":
		return "external_idp"
	default:
		return lower
	}
}

func inferKiroRefreshAuthMethod(account *Account) string {
	if strings.TrimSpace(account.GetCredential("token_endpoint")) != "" && strings.TrimSpace(account.GetCredential("client_id")) != "" {
		return "external_idp"
	}
	if strings.TrimSpace(account.GetCredential("client_id")) != "" && strings.TrimSpace(account.GetCredential("client_secret")) != "" {
		return "idc"
	}
	return "social"
}

func refreshKiroOIDCToken(ctx context.Context, client *http.Client, account *Account, refreshToken string) (*KiroTokenInfo, error) {
	clientID := strings.TrimSpace(account.GetCredential("client_id"))
	clientSecret := strings.TrimSpace(account.GetCredential("client_secret"))
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("OIDC refresh requires client_id and client_secret")
	}
	region := strings.TrimSpace(account.GetCredential("region"))
	if region == "" {
		region = "us-east-1"
	}
	tokenInfo, err := refreshKiroOIDCTokenInRegion(ctx, client, region, clientID, clientSecret, refreshToken)
	if err == nil || region == "us-east-1" {
		return tokenInfo, err
	}
	fallback, fallbackErr := refreshKiroOIDCTokenInRegion(ctx, client, "us-east-1", clientID, clientSecret, refreshToken)
	if fallbackErr == nil {
		return fallback, nil
	}
	return nil, fmt.Errorf("OIDC refresh failed in %s: %v; fallback us-east-1: %w", region, err, fallbackErr)
}

func refreshKiroOIDCTokenInRegion(ctx context.Context, client *http.Client, region, clientID, clientSecret, refreshToken string) (*KiroTokenInfo, error) {
	payload := map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"refreshToken": refreshToken,
		"grantType":    "refresh_token",
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("https://oidc.%s.amazonaws.com/token", region), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("refresh failed: %d %s", resp.StatusCode, string(respBody))
	}
	var result struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		ProfileArn   string `json:"profileArn"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return newKiroTokenInfo(result.AccessToken, result.RefreshToken, result.ExpiresIn, result.ProfileArn), nil
}

func refreshKiroSocialToken(ctx context.Context, client *http.Client, refreshToken string) (*KiroTokenInfo, error) {
	payload := map[string]string{"refreshToken": refreshToken}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("refresh failed: %d %s", resp.StatusCode, string(respBody))
	}
	var result struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		ProfileArn   string `json:"profileArn"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return newKiroTokenInfo(result.AccessToken, result.RefreshToken, result.ExpiresIn, result.ProfileArn), nil
}

func refreshKiroExternalIDPToken(ctx context.Context, client *http.Client, account *Account, refreshToken string) (*KiroTokenInfo, error) {
	clientID := strings.TrimSpace(account.GetCredential("client_id"))
	tokenEndpoint := strings.TrimSpace(account.GetCredential("token_endpoint"))
	if clientID == "" {
		return nil, fmt.Errorf("external IdP refresh requires client_id")
	}
	if tokenEndpoint == "" {
		return nil, fmt.Errorf("external IdP refresh requires token_endpoint")
	}
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	if scope := strings.TrimSpace(account.GetCredential("scope")); scope != "" {
		form.Set("scope", scope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		ExpiresIn        int64  `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(respBody, &result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || result.AccessToken == "" {
		if result.Error != "" {
			return nil, fmt.Errorf("external IdP refresh failed: %d %s: %s", resp.StatusCode, result.Error, result.ErrorDescription)
		}
		return nil, fmt.Errorf("external IdP refresh failed: %d %s", resp.StatusCode, string(respBody))
	}
	return newKiroTokenInfo(result.AccessToken, result.RefreshToken, result.ExpiresIn, ""), nil
}

func newKiroTokenInfo(accessToken, refreshToken string, expiresIn int64, profileArn string) *KiroTokenInfo {
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return &KiroTokenInfo{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    expiresIn,
		ExpiresAt:    time.Now().Unix() + expiresIn,
		ProfileArn:   profileArn,
		TokenType:    "Bearer",
	}
}
