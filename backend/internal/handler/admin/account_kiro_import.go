package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// KiroImportRequest represents the request body for importing Kiro accounts.
type KiroImportRequest struct {
	Data                any    `json:"data"`
	Name                string `json:"name"`
	Notes               string `json:"notes"`
	GroupIDs            []int64 `json:"group_ids"`
	ProxyID             *int64 `json:"proxy_id"`
	Concurrency         *int   `json:"concurrency"`
	Priority            *int   `json:"priority"`
	RateMultiplier      *float64 `json:"rate_multiplier"`
	LoadFactor          *int   `json:"load_factor"`
	ExpiresAt           *int64 `json:"expires_at"`
	AutoPauseOnExpired  *bool  `json:"auto_pause_on_expired"`
	SkipDefaultGroupBind *bool `json:"skip_default_group_bind"`
}

// KiroImportResult represents the result of importing Kiro accounts.
type KiroImportResult struct {
	Total   int                  `json:"total"`
	Created int                  `json:"created"`
	Failed  int                  `json:"failed"`
	Items   []KiroImportItem     `json:"items,omitempty"`
	Errors  []KiroImportMessage  `json:"errors,omitempty"`
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
	Name         string         `json:"name"`
	RefreshToken string         `json:"refresh_token"`
	ClientID     string         `json:"client_id"`
	ClientSecret string         `json:"client_secret"`
	Region       string         `json:"region"`
	ProfileArn   string         `json:"profile_arn"`
	TokenEndpoint string        `json:"token_endpoint"`
	IssuerURL    string         `json:"issuer_url"`
	Scopes       []string       `json:"scopes"`
	ExternalIDP  map[string]any `json:"external_idp"`
	RawData      map[string]any `json:"-"`
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
//   - Enterprise external_idp JSON
func parseKiroImportData(data any) ([]kiroAccountData, error) {
	// Convert to JSON bytes for parsing
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input data: %w", err)
	}

	// Try to parse as a single account first
	var singleAccount kiroAccountData
	if err := json.Unmarshal(jsonBytes, &singleAccount); err == nil {
		if singleAccount.RefreshToken != "" || singleAccount.ClientID != "" {
			return []kiroAccountData{singleAccount}, nil
		}
	}

	// Try to parse as an array of accounts
	var accounts []kiroAccountData
	if err := json.Unmarshal(jsonBytes, &accounts); err == nil {
		if len(accounts) > 0 {
			return accounts, nil
		}
	}

	// Try to parse as Kiro Account Manager wrapped data
	var wrappedData struct {
		Accounts []kiroAccountData `json:"accounts"`
	}
	if err := json.Unmarshal(jsonBytes, &wrappedData); err == nil {
		if len(wrappedData.Accounts) > 0 {
			return wrappedData.Accounts, nil
		}
	}

	// Try to parse as enterprise external_idp JSON
	var externalIDPData struct {
		ExternalIDP map[string]any `json:"external_idp"`
		RefreshToken string         `json:"refresh_token"`
		ClientID     string         `json:"client_id"`
		ClientSecret string         `json:"client_secret"`
		Region       string         `json:"region"`
		ProfileArn   string         `json:"profile_arn"`
		TokenEndpoint string        `json:"token_endpoint"`
		IssuerURL    string         `json:"issuer_url"`
		Scopes       []string       `json:"scopes"`
	}
	if err := json.Unmarshal(jsonBytes, &externalIDPData); err == nil {
		if externalIDPData.ExternalIDP != nil {
			// This is an enterprise external_idp JSON
			account := kiroAccountData{
				RefreshToken:  externalIDPData.RefreshToken,
				ClientID:      externalIDPData.ClientID,
				ClientSecret:  externalIDPData.ClientSecret,
				Region:        externalIDPData.Region,
				ProfileArn:    externalIDPData.ProfileArn,
				TokenEndpoint: externalIDPData.TokenEndpoint,
				IssuerURL:     externalIDPData.IssuerURL,
				Scopes:        externalIDPData.Scopes,
				ExternalIDP:   externalIDPData.ExternalIDP,
			}
			return []kiroAccountData{account}, nil
		}
	}

	// Try to parse as a map and extract accounts from various keys
	var mapData map[string]any
	if err := json.Unmarshal(jsonBytes, &mapData); err == nil {
		// Check for "data" key (common wrapper)
		if dataKey, ok := mapData["data"]; ok {
			return parseKiroImportData(dataKey)
		}

		// Check for "items" key
		if itemsKey, ok := mapData["items"]; ok {
			return parseKiroImportData(itemsKey)
		}

		// Check for "accounts" key (might be a different format)
		if accountsKey, ok := mapData["accounts"]; ok {
			return parseKiroImportData(accountsKey)
		}
	}

	return nil, fmt.Errorf("unable to parse Kiro account data from input")
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
		if account.ClientID != "" {
			credentials["client_id"] = account.ClientID
		}
		if account.ClientSecret != "" {
			credentials["client_secret"] = account.ClientSecret
		}
		if account.Region != "" {
			credentials["region"] = account.Region
		}
		if account.ProfileArn != "" {
			credentials["profile_arn"] = account.ProfileArn
		}

		// Build extra
		extra := map[string]any{}
		if account.TokenEndpoint != "" {
			extra["token_endpoint"] = account.TokenEndpoint
		}
		if account.IssuerURL != "" {
			extra["issuer_url"] = account.IssuerURL
		}
		if len(account.Scopes) > 0 {
			extra["scopes"] = account.Scopes
		}
		if account.ExternalIDP != nil {
			extra["external_idp"] = account.ExternalIDP
		}

		// Merge any extra from request
		if req.Notes != "" {
			extra["notes"] = req.Notes
		}

		// Build account name
		accountName := account.Name
		if accountName == "" {
			if account.ProfileArn != "" {
				// Extract name from profile ARN
				parts := strings.Split(account.ProfileArn, "/")
				if len(parts) > 0 {
					accountName = parts[len(parts)-1]
				}
			}
			if accountName == "" && account.ClientID != "" {
				accountName = fmt.Sprintf("kiro-%s", account.ClientID[:8])
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
			Name:           accountName,
			Platform:       domain.PlatformKiro,
			Type:           "oauth",
			Credentials:    credentials,
			Extra:          extra,
			Concurrency:    concurrency,
			Priority:       priority,
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
