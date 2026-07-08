package admin

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type GrokOAuthHandler struct {
	adminService     service.AdminService
	grokOAuthService *service.GrokOAuthService
}

func NewGrokOAuthHandler(adminService service.AdminService, grokOAuthService *service.GrokOAuthService) *GrokOAuthHandler {
	return &GrokOAuthHandler{adminService: adminService, grokOAuthService: grokOAuthService}
}

type GrokOAuthCreateRequest struct {
	Name                    string   `json:"name"`
	Notes                   *string  `json:"notes"`
	Data                    any      `json:"data" binding:"required"`
	ProxyID                 *int64   `json:"proxy_id"`
	Concurrency             *int     `json:"concurrency"`
	Priority                *int     `json:"priority"`
	RateMultiplier          *float64 `json:"rate_multiplier"`
	LoadFactor              *int     `json:"load_factor"`
	GroupIDs                []int64  `json:"group_ids"`
	ExpiresAt               *int64   `json:"expires_at"`
	AutoPauseOnExpired      *bool    `json:"auto_pause_on_expired"`
	SkipDefaultGroupBind    *bool    `json:"skip_default_group_bind"`
	ConfirmMixedChannelRisk *bool    `json:"confirm_mixed_channel_risk"`
}

type GrokCreateFromOAuthRequest struct {
	SessionID               string   `json:"session_id" binding:"required"`
	Code                    string   `json:"code" binding:"required"`
	State                   string   `json:"state" binding:"required"`
	RedirectURI             string   `json:"redirect_uri"`
	Name                    string   `json:"name"`
	Notes                   *string  `json:"notes"`
	ProxyID                 *int64   `json:"proxy_id"`
	Concurrency             *int     `json:"concurrency"`
	Priority                *int     `json:"priority"`
	RateMultiplier          *float64 `json:"rate_multiplier"`
	LoadFactor              *int     `json:"load_factor"`
	GroupIDs                []int64  `json:"group_ids"`
	ExpiresAt               *int64   `json:"expires_at"`
	AutoPauseOnExpired      *bool    `json:"auto_pause_on_expired"`
	SkipDefaultGroupBind    *bool    `json:"skip_default_group_bind"`
	ConfirmMixedChannelRisk *bool    `json:"confirm_mixed_channel_risk"`
}

type GrokOAuthNormalizeRequest struct {
	Data any `json:"data" binding:"required"`
}

type GrokOAuthNormalizeResponse struct {
	Name        string         `json:"name"`
	Credentials map[string]any `json:"credentials"`
	Extra       map[string]any `json:"extra"`
}

type GrokGenerateAuthURLRequest struct {
	ProxyID     *int64 `json:"proxy_id"`
	RedirectURI string `json:"redirect_uri"`
}

type GrokExchangeCodeRequest struct {
	SessionID   string `json:"session_id" binding:"required"`
	Code        string `json:"code" binding:"required"`
	State       string `json:"state" binding:"required"`
	RedirectURI string `json:"redirect_uri"`
	ProxyID     *int64 `json:"proxy_id"`
}

type GrokRefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
	RT           string `json:"rt"`
	ProxyID      *int64 `json:"proxy_id"`
}

type grokTokenPayload struct {
	AccessToken   string
	RefreshToken  string
	IDToken       string
	TokenType     string
	ExpiresAt     int64
	Scope         string
	ScopeList     []string
	ClientID      string
	TokenEndpoint string
	APIBaseURL    string
	Email         string
	Subject       string
	Username      string
	DisplayName   string
}

// GenerateAuthURL creates an xAI OAuth authorization URL.
// POST /api/v1/admin/grok/oauth/generate-auth-url
func (h *GrokOAuthHandler) GenerateAuthURL(c *gin.Context) {
	var req GrokGenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req = GrokGenerateAuthURLRequest{}
	}
	result, err := h.grokOAuthService.GenerateAuthURL(c.Request.Context(), req.ProxyID, req.RedirectURI)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// ExchangeCode exchanges an OAuth authorization code for Grok tokens.
// POST /api/v1/admin/grok/oauth/exchange-code
func (h *GrokOAuthHandler) ExchangeCode(c *gin.Context) {
	var req GrokExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	info, err := h.grokOAuthService.ExchangeCode(c.Request.Context(), &service.GrokExchangeCodeInput{
		SessionID:   req.SessionID,
		Code:        req.Code,
		State:       req.State,
		RedirectURI: req.RedirectURI,
		ProxyID:     req.ProxyID,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, info)
}

// RefreshToken refreshes a Grok OAuth token by refresh_token.
// POST /api/v1/admin/grok/oauth/refresh-token
func (h *GrokOAuthHandler) RefreshToken(c *gin.Context) {
	var req GrokRefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	refreshToken := strings.TrimSpace(req.RefreshToken)
	if refreshToken == "" {
		refreshToken = strings.TrimSpace(req.RT)
	}
	if refreshToken == "" {
		response.BadRequest(c, "refresh_token is required")
		return
	}
	var proxyURL string
	if req.ProxyID != nil {
		proxy, err := h.adminService.GetProxy(c.Request.Context(), *req.ProxyID)
		if err == nil && proxy != nil {
			proxyURL = proxy.URL()
		}
	}
	info, err := h.grokOAuthService.RefreshToken(c.Request.Context(), refreshToken, proxyURL)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, info)
}

// RefreshAccountToken refreshes tokens for a saved Grok OAuth account.
// POST /api/v1/admin/grok/oauth/accounts/:id/refresh
func (h *GrokOAuthHandler) RefreshAccountToken(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := requireGrokOAuthAccount(account); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	info, err := h.grokOAuthService.RefreshAccountToken(c.Request.Context(), account)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	credentials := service.MergeCredentials(account.Credentials, h.grokOAuthService.BuildAccountCredentials(info))
	updatedAccount, err := h.adminService.UpdateAccount(c.Request.Context(), accountID, &service.UpdateAccountInput{Credentials: credentials})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, updatedAccount)
}

// CreateAccountFromOAuth creates a Grok OAuth account from a code-flow token exchange.
// POST /api/v1/admin/grok/oauth/create-from-oauth
func (h *GrokOAuthHandler) CreateAccountFromOAuth(c *gin.Context) {
	var req GrokCreateFromOAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	payload, err := h.exchangeOAuthPayload(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	account, err := h.createGrokOAuthAccount(c.Request.Context(), GrokOAuthCreateRequest{
		Name:                    req.Name,
		Notes:                   req.Notes,
		ProxyID:                 req.ProxyID,
		Concurrency:             req.Concurrency,
		Priority:                req.Priority,
		RateMultiplier:          req.RateMultiplier,
		LoadFactor:              req.LoadFactor,
		GroupIDs:                req.GroupIDs,
		ExpiresAt:               req.ExpiresAt,
		AutoPauseOnExpired:      req.AutoPauseOnExpired,
		SkipDefaultGroupBind:    req.SkipDefaultGroupBind,
		ConfirmMixedChannelRisk: req.ConfirmMixedChannelRisk,
	}, payload)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, account)
}

// NormalizeCredentials converts a single Grok/xAI token JSON payload into account credentials.
// POST /api/v1/admin/grok/oauth/normalize
func (h *GrokOAuthHandler) NormalizeCredentials(c *gin.Context) {
	var req GrokOAuthNormalizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	payload, err := parseSingleGrokTokenPayload(req.Data)
	if err != nil {
		response.BadRequest(c, "Failed to parse Grok token data: "+err.Error())
		return
	}

	name, credentials, extra := buildGrokOAuthAccountPayload(payload, "")
	response.Success(c, GrokOAuthNormalizeResponse{Name: name, Credentials: credentials, Extra: extra})
}

// CreateAccount creates one Grok OAuth account from a manual token JSON payload.
// POST /api/v1/admin/grok/oauth/create-account
func (h *GrokOAuthHandler) CreateAccount(c *gin.Context) {
	var req GrokOAuthCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	payload, err := parseSingleGrokTokenPayload(req.Data)
	if err != nil {
		response.BadRequest(c, "Failed to parse Grok token data: "+err.Error())
		return
	}

	account, err := h.createGrokOAuthAccount(c.Request.Context(), req, payload)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, account)
}

func (h *GrokOAuthHandler) createGrokOAuthAccount(ctx context.Context, req GrokOAuthCreateRequest, payload grokTokenPayload) (*service.Account, error) {
	name, credentials, extra := buildGrokOAuthAccountPayload(payload, req.Name)
	if req.Notes != nil && strings.TrimSpace(*req.Notes) != "" {
		extra["notes"] = strings.TrimSpace(*req.Notes)
	}

	concurrency := 3
	if req.Concurrency != nil {
		concurrency = *req.Concurrency
	}
	priority := 50
	if req.Priority != nil {
		priority = *req.Priority
	}

	input := &service.CreateAccountInput{
		Name:                  name,
		Notes:                 req.Notes,
		Platform:              service.PlatformGrok,
		Type:                  service.AccountTypeOAuth,
		Credentials:           credentials,
		Extra:                 extra,
		ProxyID:               req.ProxyID,
		Concurrency:           concurrency,
		Priority:              priority,
		RateMultiplier:        req.RateMultiplier,
		LoadFactor:            req.LoadFactor,
		GroupIDs:              req.GroupIDs,
		ExpiresAt:             req.ExpiresAt,
		AutoPauseOnExpired:    req.AutoPauseOnExpired,
		SkipMixedChannelCheck: req.ConfirmMixedChannelRisk != nil && *req.ConfirmMixedChannelRisk,
	}
	if req.SkipDefaultGroupBind != nil {
		input.SkipDefaultGroupBind = *req.SkipDefaultGroupBind
	}
	return h.adminService.CreateAccount(ctx, input)
}

func (h *GrokOAuthHandler) exchangeOAuthPayload(ctx context.Context, req GrokCreateFromOAuthRequest) (grokTokenPayload, error) {
	tokenInfo, err := h.grokOAuthService.ExchangeCode(ctx, &service.GrokExchangeCodeInput{
		SessionID:   strings.TrimSpace(req.SessionID),
		Code:        strings.TrimSpace(req.Code),
		State:       strings.TrimSpace(req.State),
		RedirectURI: strings.TrimSpace(req.RedirectURI),
		ProxyID:     req.ProxyID,
	})
	if err != nil {
		return grokTokenPayload{}, err
	}
	return grokTokenPayload{
		AccessToken:   tokenInfo.AccessToken,
		RefreshToken:  tokenInfo.RefreshToken,
		IDToken:       tokenInfo.IDToken,
		TokenType:     tokenInfo.TokenType,
		ExpiresAt:     tokenInfo.ExpiresAt,
		Scope:         tokenInfo.Scope,
		ScopeList:     tokenInfo.Scopes,
		ClientID:      tokenInfo.ClientID,
		TokenEndpoint: tokenInfo.TokenEndpoint,
		APIBaseURL:    tokenInfo.APIBaseURL,
		Email:         tokenInfo.Email,
		Subject:       tokenInfo.Subject,
		DisplayName:   tokenInfo.Name,
	}, nil
}

func buildGrokOAuthAccountPayload(payload grokTokenPayload, requestedName string) (string, map[string]any, map[string]any) {
	credentials := map[string]any{}
	putString := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			credentials[key] = strings.TrimSpace(value)
		}
	}
	putString("access_token", payload.AccessToken)
	putString("refresh_token", payload.RefreshToken)
	putString("id_token", payload.IDToken)
	putString("token_type", payload.TokenType)
	putString("client_id", payload.ClientID)
	putString("token_endpoint", payload.TokenEndpoint)
	putString("api_base_url", payload.APIBaseURL)
	if payload.ExpiresAt > 0 {
		credentials["expires_at"] = payload.ExpiresAt
	}
	if len(payload.ScopeList) > 0 {
		credentials["scopes"] = payload.ScopeList
	}
	if payload.Scope != "" {
		credentials["scope"] = payload.Scope
	}

	extra := map[string]any{
		"auth_provider":  "xai",
		"oauth_provider": "xai",
		"created_from":   "manual_token_json",
	}
	if strings.TrimSpace(payload.RefreshToken) != "" && strings.TrimSpace(payload.ClientID) == service.GrokOAuthClientID {
		extra["created_from"] = "oauth_code_flow"
	}
	putExtraString := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			extra[key] = strings.TrimSpace(value)
		}
	}
	putExtraString("email", payload.Email)
	putExtraString("subject", payload.Subject)
	putExtraString("username", payload.Username)
	putExtraString("display_name", payload.DisplayName)
	putExtraString("api_base_url", payload.APIBaseURL)
	if len(payload.ScopeList) > 0 {
		extra["scope_list"] = payload.ScopeList
	}

	name := strings.TrimSpace(requestedName)
	if name == "" {
		name = inferGrokOAuthAccountName(payload)
	}
	return name, credentials, extra
}

func inferGrokOAuthAccountName(payload grokTokenPayload) string {
	if strings.TrimSpace(payload.DisplayName) != "" {
		return strings.TrimSpace(payload.DisplayName)
	}
	if strings.TrimSpace(payload.Username) != "" {
		return strings.TrimSpace(payload.Username)
	}
	if strings.TrimSpace(payload.Email) != "" {
		return strings.TrimSpace(payload.Email)
	}
	if strings.TrimSpace(payload.Subject) != "" {
		subject := strings.TrimSpace(payload.Subject)
		if len(subject) > 12 {
			subject = subject[:12]
		}
		return fmt.Sprintf("grok-%s", subject)
	}
	return "grok-account"
}

func parseSingleGrokTokenPayload(data any) (grokTokenPayload, error) {
	root, ok := data.(map[string]any)
	if !ok || root == nil {
		return grokTokenPayload{}, fmt.Errorf("expected a JSON object payload")
	}
	if len(root) == 0 {
		return grokTokenPayload{}, fmt.Errorf("empty payload")
	}

	candidates := collectGrokPayloadCandidates(root)
	payload := grokTokenPayload{
		AccessToken:   readFirstStringFromMaps(candidates, "access_token", "accessToken"),
		RefreshToken:  readFirstStringFromMaps(candidates, "refresh_token", "refreshToken"),
		IDToken:       readFirstStringFromMaps(candidates, "id_token", "idToken"),
		TokenType:     readFirstStringFromMaps(candidates, "token_type", "tokenType"),
		ClientID:      readFirstStringFromMaps(candidates, "client_id", "clientId"),
		TokenEndpoint: readFirstStringFromMaps(candidates, "token_endpoint", "tokenEndpoint"),
		APIBaseURL:    readFirstStringFromMaps(candidates, "api_base_url", "apiBaseUrl", "base_url", "baseUrl"),
		Email:         readFirstStringFromMaps(candidates, "email"),
		Subject:       readFirstStringFromMaps(candidates, "sub", "subject"),
		Username:      readFirstStringFromMaps(candidates, "username", "name"),
		DisplayName:   readFirstStringFromMaps(candidates, "display_name", "displayName"),
	}

	if payload.TokenType == "" {
		payload.TokenType = "Bearer"
	}
	if isPlaceholderToken(payload.AccessToken) {
		return grokTokenPayload{}, fmt.Errorf("access_token is required and must not be a placeholder")
	}

	expiresAt, err := resolveExpiresAtFromMaps(candidates)
	if err != nil {
		return grokTokenPayload{}, err
	}
	payload.ExpiresAt = expiresAt
	if payload.ExpiresAt > 0 && payload.ExpiresAt <= time.Now().UTC().Add(-time.Minute).Unix() {
		return grokTokenPayload{}, fmt.Errorf("token is already expired")
	}

	payload.ScopeList = readScopeListFromMaps(candidates)
	if len(payload.ScopeList) > 0 {
		payload.Scope = strings.Join(payload.ScopeList, " ")
	}

	return payload, nil
}

func collectGrokPayloadCandidates(root map[string]any) []map[string]any {
	candidates := make([]map[string]any, 0, 5)
	appendIfMap := func(value any) {
		if mapped, ok := asMap(value); ok {
			candidates = append(candidates, mapped)
		}
	}

	appendIfMap(root["credentials"])
	appendIfMap(root["token"])
	appendIfMap(root["user"])
	if data, ok := asMap(root["data"]); ok {
		appendIfMap(data["credentials"])
		appendIfMap(data["token"])
		appendIfMap(data["user"])
		candidates = append(candidates, data)
	}
	candidates = append(candidates, root)
	return candidates
}

func asMap(value any) (map[string]any, bool) {
	mapped, ok := value.(map[string]any)
	return mapped, ok && mapped != nil
}

func readFirstString(source map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := source[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				return trimmed
			}
		case fmt.Stringer:
			if trimmed := strings.TrimSpace(typed.String()); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func readFirstStringFromMaps(sources []map[string]any, keys ...string) string {
	for _, source := range sources {
		if value := readFirstString(source, keys...); value != "" {
			return value
		}
	}
	return ""
}

func readScopeListFromMaps(sources []map[string]any) []string {
	for _, source := range sources {
		if scopes := readScopeList(source); len(scopes) > 0 {
			return scopes
		}
	}
	return nil
}

func readScopeList(source map[string]any) []string {
	if value, ok := source["scopes"]; ok && value != nil {
		switch typed := value.(type) {
		case []any:
			list := make([]string, 0, len(typed))
			for _, item := range typed {
				text := strings.TrimSpace(fmt.Sprint(item))
				if text != "" {
					list = append(list, text)
				}
			}
			if len(list) > 0 {
				return dedupeStrings(list)
			}
		case []string:
			return dedupeStrings(typed)
		case string:
			if typed != "" {
				return splitScopeString(typed)
			}
		}
	}
	return splitScopeString(readFirstString(source, "scope"))
}

func splitScopeString(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ' ' || r == ','
	})
	return dedupeStrings(fields)
}

func dedupeStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func resolveExpiresAtFromMaps(sources []map[string]any) (int64, error) {
	for _, source := range sources {
		unix, err := resolveExpiresAt(source)
		if err != nil {
			return 0, err
		}
		if unix > 0 {
			return unix, nil
		}
	}
	return 0, nil
}

func resolveExpiresAt(source map[string]any) (int64, error) {
	if value, ok := source["expires_at"]; ok && value != nil {
		unix, err := parseExpiresAtValue(value)
		if err != nil {
			return 0, fmt.Errorf("invalid expires_at: %w", err)
		}
		if unix > 0 {
			return unix, nil
		}
	}
	if value, ok := source["expiresAt"]; ok && value != nil {
		unix, err := parseExpiresAtValue(value)
		if err != nil {
			return 0, fmt.Errorf("invalid expiresAt: %w", err)
		}
		if unix > 0 {
			return unix, nil
		}
	}
	if value, ok := source["expires_in"]; ok && value != nil {
		seconds, err := parseDurationSeconds(value)
		if err != nil {
			return 0, fmt.Errorf("invalid expires_in: %w", err)
		}
		if seconds > 0 {
			return time.Now().UTC().Add(time.Duration(seconds) * time.Second).Unix(), nil
		}
	}
	if value, ok := source["expiresIn"]; ok && value != nil {
		seconds, err := parseDurationSeconds(value)
		if err != nil {
			return 0, fmt.Errorf("invalid expiresIn: %w", err)
		}
		if seconds > 0 {
			return time.Now().UTC().Add(time.Duration(seconds) * time.Second).Unix(), nil
		}
	}
	return 0, nil
}

func parseExpiresAtValue(value any) (int64, error) {
	switch typed := value.(type) {
	case float64:
		return normalizeUnixTimestamp(int64(math.Round(typed))), nil
	case float32:
		return normalizeUnixTimestamp(int64(math.Round(float64(typed)))), nil
	case int64:
		return normalizeUnixTimestamp(typed), nil
	case int:
		return normalizeUnixTimestamp(int64(typed)), nil
	case int32:
		return normalizeUnixTimestamp(int64(typed)), nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, nil
		}
		if unix, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return normalizeUnixTimestamp(unix), nil
		}
		parsedTime, err := time.Parse(time.RFC3339, trimmed)
		if err != nil {
			return 0, err
		}
		return parsedTime.UTC().Unix(), nil
	default:
		return 0, fmt.Errorf("unsupported expires_at type %T", value)
	}
}

func parseDurationSeconds(value any) (int64, error) {
	switch typed := value.(type) {
	case float64:
		return int64(math.Round(typed)), nil
	case float32:
		return int64(math.Round(float64(typed))), nil
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, nil
		}
		return strconv.ParseInt(trimmed, 10, 64)
	default:
		return 0, fmt.Errorf("unsupported expires_in type %T", value)
	}
}

func normalizeUnixTimestamp(value int64) int64 {
	if value <= 0 {
		return 0
	}
	if value >= 1_000_000_000_000 {
		return value / 1000
	}
	return value
}

func isPlaceholderToken(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return true
	}
	lowered := strings.ToLower(trimmed)
	exactPlaceholders := map[string]struct{}{
		"your_access_token": {},
		"access_token_here": {},
		"replace_me":        {},
		"placeholder":       {},
		"dummy":             {},
		"example":           {},
		"test-token":        {},
		"token":             {},
	}
	if _, ok := exactPlaceholders[lowered]; ok {
		return true
	}
	containsPlaceholders := []string{
		"<access_token>",
		"{{access_token}}",
		"your-access-token",
		"insert-access-token",
	}
	for _, placeholder := range containsPlaceholders {
		if strings.Contains(lowered, placeholder) {
			return true
		}
	}
	return false
}

func requireGrokOAuthAccount(account *service.Account) error {
	if account == nil {
		return infraerrors.BadRequest("ACCOUNT_REQUIRED", "account is required")
	}
	if account.Platform != service.PlatformGrok || account.Type != service.AccountTypeOAuth {
		return infraerrors.BadRequest("NOT_GROK_OAUTH", "account is not a Grok OAuth account")
	}
	return nil
}
