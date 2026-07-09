package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// KiroImportRequest represents the request body for importing Kiro accounts.
type KiroImportRequest struct {
	Data                 any      `json:"data"`
	Name                 string   `json:"name"`
	Notes                string   `json:"notes"`
	GroupIDs             []int64  `json:"group_ids"`
	ProxyID              *int64   `json:"proxy_id"`
	Concurrency          *int     `json:"concurrency"`
	Priority             *int     `json:"priority"`
	RateMultiplier       *float64 `json:"rate_multiplier"`
	LoadFactor           *int     `json:"load_factor"`
	ExpiresAt            *int64   `json:"expires_at"`
	AutoPauseOnExpired   *bool    `json:"auto_pause_on_expired"`
	SkipDefaultGroupBind *bool    `json:"skip_default_group_bind"`
}

// KiroImportResult represents the result of importing Kiro accounts.
type KiroImportResult struct {
	Total   int                 `json:"total"`
	Created int                 `json:"created"`
	Failed  int                 `json:"failed"`
	Items   []KiroImportItem    `json:"items,omitempty"`
	Errors  []KiroImportMessage `json:"errors,omitempty"`
}

// KiroImportItem represents a single imported Kiro account.
type KiroImportItem struct {
	Index     int    `json:"index"`
	Name      string `json:"name,omitempty"`
	Action    string `json:"action"`
	AccountID int64  `json:"account_id,omitempty"`
	Message   string `json:"message,omitempty"`
}

// KiroImportMessage represents an error message for a Kiro import item.
type KiroImportMessage struct {
	Index   int    `json:"index"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
}

// kiroAccountData represents the parsed Kiro account data.
type kiroAccountData struct {
	Name                          string         `json:"name"`
	Email                         string         `json:"email"`
	AccessToken                   string         `json:"access_token"`
	RefreshToken                  string         `json:"refresh_token"`
	ClientID                      string         `json:"client_id"`
	ClientSecret                  string         `json:"client_secret"`
	AuthMethod                    string         `json:"auth_method"`
	Provider                      string         `json:"provider"`
	Region                        string         `json:"region"`
	ProfileArn                    string         `json:"profile_arn"`
	TokenEndpoint                 string         `json:"token_endpoint"`
	IssuerURL                     string         `json:"issuer_url"`
	Scopes                        []string       `json:"scopes"`
	StartURL                      string         `json:"start_url"`
	ExpiresAt                     int64          `json:"expires_at"`
	MachineID                     string         `json:"machine_id"`
	SubscriptionType              string         `json:"subscription_type"`
	SubscriptionTitle             string         `json:"subscription_title"`
	SubscriptionRawType           string         `json:"subscription_raw_type"`
	SubscriptionDaysRemaining     int64          `json:"subscription_days_remaining"`
	SubscriptionExpiresAt         int64          `json:"subscription_expires_at"`
	SubscriptionOverageCapability string         `json:"subscription_overage_capability"`
	UsageCurrent                  float64        `json:"usage_current"`
	UsageLimit                    float64        `json:"usage_limit"`
	UsagePercentUsed              float64        `json:"usage_percent_used"`
	UsageBaseLimit                float64        `json:"usage_base_limit"`
	UsageNextResetDate            string         `json:"usage_next_reset_date"`
	UsageOverageEnabled           bool           `json:"usage_overage_enabled"`
	UsageOverageCap               float64        `json:"usage_overage_cap"`
	UsageOverageRate              float64        `json:"usage_overage_rate"`
	ExternalIDP                   map[string]any `json:"external_idp"`
	RawData                       map[string]any `json:"-"`
}

// ImportKiroAccounts handles the import of Kiro accounts.
func (h *AccountHandler) ImportKiroAccounts(c *gin.Context) {
	var req KiroImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if req.Data == nil {
		response.BadRequest(c, "data field is required")
		return
	}

	// Parse the input data into a list of Kiro accounts
	accounts, err := parseKiroImportData(req.Data)
	if err != nil {
		response.BadRequest(c, "Failed to parse Kiro account data: "+err.Error())
		return
	}

	if len(accounts) == 0 {
		response.BadRequest(c, "No valid Kiro accounts found in the input data")
		return
	}

	executeAdminIdempotentJSON(c, "admin.accounts.import_kiro", req, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		return h.importKiroAccounts(ctx, req, accounts)
	})
}

// parseKiroImportData parses the input data into a list of Kiro accounts.
// It supports multiple input formats:
//   - Single Kiro account object
//   - Array of Kiro account objects
//   - Kiro Account Manager wrapped data (with "accounts" key)
//   - Single-account wrapped data (with "account" key)
//   - Enterprise external_idp JSON
func parseKiroImportData(data any) ([]kiroAccountData, error) {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input data: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(jsonBytes, &decoded); err != nil {
		return nil, fmt.Errorf("invalid JSON data: %w", err)
	}
	accounts, err := parseKiroImportValue(decoded)
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("unable to parse Kiro account data from input")
	}
	return accounts, nil
}

func parseKiroImportValue(value any) ([]kiroAccountData, error) {
	switch v := value.(type) {
	case []any:
		accounts := make([]kiroAccountData, 0, len(v))
		for _, item := range v {
			parsed, err := parseKiroImportValue(item)
			if err != nil {
				return nil, err
			}
			accounts = append(accounts, parsed...)
		}
		return accounts, nil
	case map[string]any:
		if account, ok := kiroAccountFromMap(v); ok {
			return []kiroAccountData{account}, nil
		}
		for _, key := range []string{"data", "account", "accounts", "items"} {
			if nested, ok := v[key]; ok {
				return parseKiroImportValue(nested)
			}
		}
	}
	return nil, fmt.Errorf("unable to parse Kiro account data from input")
}

func kiroAccountFromMap(raw map[string]any) (kiroAccountData, bool) {
	sources := []map[string]any{raw}
	if credentials, ok := raw["credentials"].(map[string]any); ok {
		sources = append([]map[string]any{credentials}, sources...)
	}
	if externalIDP, ok := readKiroRecord(raw, "external_idp", "externalIdp", "externalIDP"); ok {
		sources = append(sources, externalIDP)
	}

	account := kiroAccountData{
		Name:          readKiroString(sources, "name", "displayName", "display_name"),
		Email:         readKiroString(sources, "email", "emailAddress", "email_address"),
		AccessToken:   readKiroString(sources, "accessToken", "access_token"),
		RefreshToken:  readKiroString(sources, "refreshToken", "refresh_token"),
		ClientID:      readKiroString(sources, "clientId", "client_id"),
		ClientSecret:  readKiroString(sources, "clientSecret", "client_secret"),
		AuthMethod:    normalizeKiroAuthMethod(readKiroString(sources, "authMethod", "auth_method", "tokenType", "token_type")),
		Provider:      readKiroString(sources, "provider", "idp"),
		Region:        readKiroString(sources, "region"),
		ProfileArn:    readKiroString(sources, "profileArn", "profile_arn"),
		TokenEndpoint: readKiroString(sources, "tokenEndpoint", "token_endpoint"),
		IssuerURL:     readKiroString(sources, "issuerUrl", "issuer_url"),
		Scopes:        readKiroScopes(sources, "scopes", "scope"),
		StartURL:      readKiroString(sources, "startUrl", "start_url"),
		ExpiresAt:     readKiroInt64(sources, "expiresAt", "expires_at"),
		MachineID:     readKiroString(sources, "machineId", "machine_id"),
		RawData:       raw,
	}
	if externalIDP, ok := readKiroRecord(raw, "external_idp", "externalIdp", "externalIDP"); ok {
		account.ExternalIDP = externalIDP
	}
	if account.Region == "" {
		account.Region = "us-east-1"
	}
	if account.AuthMethod == "" {
		account.AuthMethod = inferKiroAuthMethod(account)
	}
	if subscription, ok := readKiroRecord(raw, "subscription"); ok {
		account.SubscriptionType = readKiroString([]map[string]any{subscription}, "type")
		account.SubscriptionTitle = readKiroString([]map[string]any{subscription}, "title")
		account.SubscriptionRawType = readKiroString([]map[string]any{subscription}, "rawType", "raw_type")
		account.SubscriptionDaysRemaining = readKiroInt64([]map[string]any{subscription}, "daysRemaining", "days_remaining")
		account.SubscriptionExpiresAt = readKiroInt64([]map[string]any{subscription}, "expiresAt", "expires_at")
		account.SubscriptionOverageCapability = readKiroString([]map[string]any{subscription}, "overageCapability", "overage_capability")
	}
	if usage, ok := readKiroRecord(raw, "usage", "quota", "quotaUsage", "quota_usage"); ok {
		account.UsageCurrent = readKiroFloat64([]map[string]any{usage}, "current")
		account.UsageLimit = readKiroFloat64([]map[string]any{usage}, "limit")
		account.UsagePercentUsed = readKiroFloat64([]map[string]any{usage}, "percentUsed", "percent_used")
		account.UsageBaseLimit = readKiroFloat64([]map[string]any{usage}, "baseLimit", "base_limit")
		account.UsageNextResetDate = readKiroString([]map[string]any{usage}, "nextResetDate", "next_reset_date")
		if resourceDetail, ok := readKiroRecord(usage, "resourceDetail", "resource_detail"); ok {
			account.UsageOverageEnabled = readKiroBool([]map[string]any{resourceDetail}, "overageEnabled", "overage_enabled")
			account.UsageOverageCap = readKiroFloat64([]map[string]any{resourceDetail}, "overageCap", "overage_cap")
			account.UsageOverageRate = readKiroFloat64([]map[string]any{resourceDetail}, "overageRate", "overage_rate")
		}
	}
	if account.Provider == "" {
		account.Provider = defaultKiroProvider(account.AuthMethod)
	}

	return account, account.RefreshToken != "" || account.AccessToken != "" || account.ClientID != "" || account.ExternalIDP != nil
}

func readKiroRecord(source map[string]any, keys ...string) (map[string]any, bool) {
	for _, key := range keys {
		if value, ok := source[key].(map[string]any); ok {
			return value, true
		}
	}
	return nil, false
}

func readKiroString(sources []map[string]any, keys ...string) string {
	for _, source := range sources {
		for _, key := range keys {
			if value, ok := source[key]; ok {
				switch v := value.(type) {
				case string:
					if s := strings.TrimSpace(v); s != "" {
						return s
					}
				case json.Number:
					return v.String()
				case float64:
					return strconv.FormatInt(int64(v), 10)
				case int64:
					return strconv.FormatInt(v, 10)
				case int:
					return strconv.Itoa(v)
				}
			}
		}
	}
	return ""
}

func readKiroScopes(sources []map[string]any, keys ...string) []string {
	for _, source := range sources {
		for _, key := range keys {
			raw, ok := source[key]
			if !ok || raw == nil {
				continue
			}
			switch v := raw.(type) {
			case []any:
				scopes := make([]string, 0, len(v))
				for _, item := range v {
					if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
						scopes = append(scopes, strings.TrimSpace(s))
					}
				}
				if len(scopes) > 0 {
					return scopes
				}
			case []string:
				scopes := make([]string, 0, len(v))
				for _, item := range v {
					if strings.TrimSpace(item) != "" {
						scopes = append(scopes, strings.TrimSpace(item))
					}
				}
				if len(scopes) > 0 {
					return scopes
				}
			case string:
				parts := strings.Fields(v)
				if len(parts) > 0 {
					return parts
				}
			}
		}
	}
	return nil
}

func readKiroInt64(sources []map[string]any, keys ...string) int64 {
	value := readKiroString(sources, keys...)
	if value == "" {
		return 0
	}
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func readKiroFloat64(sources []map[string]any, keys ...string) float64 {
	for _, source := range sources {
		for _, key := range keys {
			if value, ok := source[key]; ok {
				switch v := value.(type) {
				case float64:
					return v
				case int64:
					return float64(v)
				case int:
					return float64(v)
				case json.Number:
					if f, err := v.Float64(); err == nil {
						return f
					}
				case string:
					if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
						return f
					}
				}
			}
		}
	}
	return 0
}

func readKiroBool(sources []map[string]any, keys ...string) bool {
	for _, source := range sources {
		for _, key := range keys {
			if value, ok := source[key]; ok {
				switch v := value.(type) {
				case bool:
					return v
				case string:
					if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
						return b
					}
				}
			}
		}
	}
	return false
}

func normalizeKiroAuthMethod(method string) string {
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

func inferKiroAuthMethod(account kiroAccountData) string {
	if account.TokenEndpoint != "" && account.ClientID != "" {
		return "external_idp"
	}
	if account.ClientID != "" && account.ClientSecret != "" {
		return "idc"
	}
	return "social"
}

func defaultKiroProvider(authMethod string) string {
	switch authMethod {
	case "external_idp":
		return "ExternalIdp"
	case "idc":
		return "BuilderId"
	default:
		return "Google"
	}
}

// importKiroAccounts imports the parsed Kiro accounts into the system.
func (h *AccountHandler) importKiroAccounts(ctx context.Context, req KiroImportRequest, accounts []kiroAccountData) (KiroImportResult, error) {
	result := KiroImportResult{
		Total: len(accounts),
		Items: make([]KiroImportItem, 0, len(accounts)),
	}

	concurrency := 3
	if req.Concurrency != nil {
		concurrency = *req.Concurrency
	}
	priority := 50
	if req.Priority != nil {
		priority = *req.Priority
	}
	skipDefaultGroupBind := false
	if req.SkipDefaultGroupBind != nil {
		skipDefaultGroupBind = *req.SkipDefaultGroupBind
	}

	for i, account := range accounts {
		item := KiroImportItem{
			Index: i,
			Name:  account.Name,
		}

		// Build credentials
		credentials := map[string]any{
			"refresh_token": account.RefreshToken,
		}
		if account.AccessToken != "" {
			credentials["access_token"] = account.AccessToken
		}
		if account.ClientID != "" {
			credentials["client_id"] = account.ClientID
		}
		if account.ClientSecret != "" {
			credentials["client_secret"] = account.ClientSecret
		}
		if account.AuthMethod != "" {
			credentials["auth_method"] = account.AuthMethod
		}
		if account.Provider != "" {
			credentials["provider"] = account.Provider
		}
		if account.Region != "" {
			credentials["region"] = account.Region
		}
		if account.ProfileArn != "" {
			credentials["profile_arn"] = account.ProfileArn
		}
		if account.TokenEndpoint != "" {
			credentials["token_endpoint"] = account.TokenEndpoint
		}
		if account.IssuerURL != "" {
			credentials["issuer_url"] = account.IssuerURL
		}
		if len(account.Scopes) > 0 {
			credentials["scopes"] = account.Scopes
			credentials["scope"] = strings.Join(account.Scopes, " ")
		}
		if account.StartURL != "" {
			credentials["start_url"] = account.StartURL
		}
		if account.ExpiresAt > 0 {
			credentials["expires_at"] = account.ExpiresAt
		}
		if account.MachineID != "" {
			credentials["machine_id"] = account.MachineID
		}
		if account.ExternalIDP != nil {
			credentials["external_idp"] = account.ExternalIDP
		}

		extra := map[string]any{}
		if account.Email != "" {
			extra["email"] = account.Email
		}
		if account.RawData != nil {
			extra["kiro_import_format"] = "kiro-go-plus"
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
		if req.Notes != "" {
			extra["notes"] = req.Notes
		}

		// Build account name
		accountName := account.Name
		if accountName == "" {
			if account.Email != "" {
				accountName = account.Email
			}
			if accountName == "" && account.ProfileArn != "" {
				parts := strings.Split(account.ProfileArn, "/")
				if len(parts) > 0 {
					accountName = parts[len(parts)-1]
				}
			}
			if accountName == "" && account.ClientID != "" {
				clientID := account.ClientID
				if len(clientID) > 8 {
					clientID = clientID[:8]
				}
				accountName = fmt.Sprintf("kiro-%s", clientID)
			}
			if accountName == "" {
				accountName = fmt.Sprintf("kiro-account-%d", i+1)
			}
		}
		if req.Name != "" {
			accountName = fmt.Sprintf("%s-%d", req.Name, i+1)
		}

		// Create the account
		createReq := &service.CreateAccountInput{
			Name:                 accountName,
			Platform:             domain.PlatformKiro,
			Type:                 "oauth",
			Credentials:          credentials,
			Extra:                extra,
			Concurrency:          concurrency,
			Priority:             priority,
			SkipDefaultGroupBind: skipDefaultGroupBind,
		}

		if req.ProxyID != nil {
			createReq.ProxyID = req.ProxyID
		}
		if req.RateMultiplier != nil {
			createReq.RateMultiplier = req.RateMultiplier
		}
		if req.LoadFactor != nil {
			createReq.LoadFactor = req.LoadFactor
		}
		if req.ExpiresAt != nil {
			createReq.ExpiresAt = req.ExpiresAt
		}
		if req.AutoPauseOnExpired != nil {
			createReq.AutoPauseOnExpired = req.AutoPauseOnExpired
		}

		// Bind groups
		if len(req.GroupIDs) > 0 {
			createReq.GroupIDs = req.GroupIDs
		}

		account, err := h.adminService.CreateAccount(ctx, createReq)
		if err != nil {
			result.Failed++
			item.Action = "failed"
			item.Message = err.Error()
			result.Errors = append(result.Errors, KiroImportMessage{
				Index:   i,
				Name:    accountName,
				Message: err.Error(),
			})
		} else {
			result.Created++
			item.Action = "created"
			item.AccountID = account.ID
		}

		result.Items = append(result.Items, item)
	}

	return result, nil
}
