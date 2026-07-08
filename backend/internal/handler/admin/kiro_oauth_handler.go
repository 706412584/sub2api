package admin

import (
	"context"
	"fmt"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type KiroOAuthHandler struct {
	adminService service.AdminService
}

func NewKiroOAuthHandler(adminService service.AdminService) *KiroOAuthHandler {
	return &KiroOAuthHandler{adminService: adminService}
}

type KiroOAuthCreateRequest struct {
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

type KiroOAuthNormalizeRequest struct {
	Data any `json:"data" binding:"required"`
}

type KiroOAuthNormalizeResponse struct {
	Name        string         `json:"name"`
	Credentials map[string]any `json:"credentials"`
	Extra       map[string]any `json:"extra"`
}

// NormalizeCredentials converts a Kiro browser-login/export payload into account credentials.
// POST /api/v1/admin/kiro/oauth/normalize
func (h *KiroOAuthHandler) NormalizeCredentials(c *gin.Context) {
	var req KiroOAuthNormalizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	accounts, err := parseKiroImportData(req.Data)
	if err != nil {
		response.BadRequest(c, "Failed to parse Kiro login data: "+err.Error())
		return
	}
	if len(accounts) == 0 {
		response.BadRequest(c, "No valid Kiro login data found")
		return
	}
	if len(accounts) > 1 {
		response.BadRequest(c, "Expected a single Kiro account payload")
		return
	}

	name, credentials, extra := buildKiroOAuthAccountPayload(accounts[0], "")
	response.Success(c, KiroOAuthNormalizeResponse{Name: name, Credentials: credentials, Extra: extra})
}

// CreateAccount creates one Kiro OAuth account from a browser-login/export payload.
// POST /api/v1/admin/kiro/oauth/create-account
func (h *KiroOAuthHandler) CreateAccount(c *gin.Context) {
	var req KiroOAuthCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	accounts, err := parseKiroImportData(req.Data)
	if err != nil {
		response.BadRequest(c, "Failed to parse Kiro login data: "+err.Error())
		return
	}
	if len(accounts) == 0 {
		response.BadRequest(c, "No valid Kiro login data found")
		return
	}
	if len(accounts) > 1 {
		response.BadRequest(c, "Expected a single Kiro account payload; use Kiro import for batch data")
		return
	}

	account, err := h.createKiroOAuthAccount(c.Request.Context(), req, accounts[0])
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, account)
}

func (h *KiroOAuthHandler) createKiroOAuthAccount(ctx context.Context, req KiroOAuthCreateRequest, account kiroAccountData) (*service.Account, error) {
	name, credentials, extra := buildKiroOAuthAccountPayload(account, req.Name)
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
		Platform:              service.PlatformKiro,
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

func buildKiroOAuthAccountPayload(account kiroAccountData, requestedName string) (string, map[string]any, map[string]any) {
	credentials := map[string]any{}
	putString := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			credentials[key] = strings.TrimSpace(value)
		}
	}
	putString("refresh_token", account.RefreshToken)
	putString("access_token", account.AccessToken)
	putString("client_id", account.ClientID)
	putString("client_secret", account.ClientSecret)
	putString("auth_method", account.AuthMethod)
	putString("provider", account.Provider)
	putString("region", account.Region)
	putString("profile_arn", account.ProfileArn)
	putString("token_endpoint", account.TokenEndpoint)
	putString("issuer_url", account.IssuerURL)
	putString("start_url", account.StartURL)
	putString("machine_id", account.MachineID)
	if len(account.Scopes) > 0 {
		credentials["scopes"] = account.Scopes
		credentials["scope"] = strings.Join(account.Scopes, " ")
	}
	if account.ExpiresAt > 0 {
		credentials["expires_at"] = account.ExpiresAt
	}
	if account.ExternalIDP != nil {
		credentials["external_idp"] = account.ExternalIDP
	}

	extra := map[string]any{"kiro_import_format": "browser-login"}
	if strings.TrimSpace(account.Email) != "" {
		extra["email"] = strings.TrimSpace(account.Email)
	}
	if account.SubscriptionType != "" || account.SubscriptionTitle != "" || account.SubscriptionRawType != "" {
		extra["kiro_subscription"] = map[string]any{
			"type":               account.SubscriptionType,
			"title":              account.SubscriptionTitle,
			"raw_type":           account.SubscriptionRawType,
			"days_remaining":     account.SubscriptionDaysRemaining,
			"expires_at":         account.SubscriptionExpiresAt,
			"overage_capability": account.SubscriptionOverageCapability,
		}
	}
	if account.UsageLimit > 0 || account.UsageCurrent > 0 || account.UsageOverageCap > 0 {
		extra["kiro_usage"] = map[string]any{
			"current":         account.UsageCurrent,
			"limit":           account.UsageLimit,
			"percent_used":    account.UsagePercentUsed,
			"base_limit":      account.UsageBaseLimit,
			"next_reset_date": account.UsageNextResetDate,
			"overage_enabled": account.UsageOverageEnabled,
			"overage_cap":     account.UsageOverageCap,
			"overage_rate":    account.UsageOverageRate,
		}
	}

	name := strings.TrimSpace(requestedName)
	if name == "" {
		name = inferKiroOAuthAccountName(account)
	}
	return name, credentials, extra
}

func inferKiroOAuthAccountName(account kiroAccountData) string {
	if strings.TrimSpace(account.Name) != "" {
		return strings.TrimSpace(account.Name)
	}
	if strings.TrimSpace(account.Email) != "" {
		return strings.TrimSpace(account.Email)
	}
	if strings.TrimSpace(account.ProfileArn) != "" {
		parts := strings.Split(account.ProfileArn, "/")
		if len(parts) > 0 && strings.TrimSpace(parts[len(parts)-1]) != "" {
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	if strings.TrimSpace(account.ClientID) != "" {
		clientID := strings.TrimSpace(account.ClientID)
		if len(clientID) > 8 {
			clientID = clientID[:8]
		}
		return fmt.Sprintf("kiro-%s", clientID)
	}
	return "kiro-account"
}

func requireKiroOAuthAccount(account *service.Account) error {
	if account == nil {
		return infraerrors.BadRequest("ACCOUNT_REQUIRED", "account is required")
	}
	if account.Platform != service.PlatformKiro || account.Type != service.AccountTypeOAuth {
		return infraerrors.BadRequest("NOT_KIRO_OAUTH", "account is not a Kiro OAuth account")
	}
	return nil
}
