package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupGrokOAuthRouter() (*gin.Engine, *stubAdminService) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	adminSvc := newStubAdminService()
	grokOAuthService := service.NewGrokOAuthService(nil)
	handler := NewGrokOAuthHandler(adminSvc, grokOAuthService)

	router.POST("/api/v1/admin/grok/oauth/normalize", handler.NormalizeCredentials)
	router.POST("/api/v1/admin/grok/oauth/create-account", handler.CreateAccount)

	return router, adminSvc
}

func TestGrokOAuthNormalizeCredentials(t *testing.T) {
	router, _ := setupGrokOAuthRouter()
	expiresAt := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	body, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"credentials": map[string]any{
				"accessToken":   "xai-access-token",
				"refreshToken":  "xai-refresh-token",
				"tokenType":     "",
				"expiresAt":     expiresAt,
				"scopes":        []string{"openid", "profile"},
				"clientId":      "client-123",
				"tokenEndpoint": "https://auth.x.ai/oauth/token",
				"apiBaseUrl":    "https://api.x.ai/v1",
				"email":         "user@example.com",
				"sub":           "subject-123",
				"username":      "grok-user",
				"display_name":  "Grok User",
			},
		},
	})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/grok/oauth/normalize", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Data struct {
			Name        string         `json:"name"`
			Credentials map[string]any `json:"credentials"`
			Extra       map[string]any `json:"extra"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "Grok User", resp.Data.Name)
	require.Equal(t, "xai-access-token", resp.Data.Credentials["access_token"])
	require.Equal(t, "Bearer", resp.Data.Credentials["token_type"])
	require.Equal(t, "https://api.x.ai/v1", resp.Data.Credentials["api_base_url"])
	require.Equal(t, "xai", resp.Data.Extra["auth_provider"])
	require.Equal(t, "manual_token_json", resp.Data.Extra["created_from"])
	require.Equal(t, "user@example.com", resp.Data.Extra["email"])
	require.NotContains(t, resp.Data.Credentials, "email")
}

func TestGrokOAuthNormalizeCredentialsRejectsExpiredToken(t *testing.T) {
	router, _ := setupGrokOAuthRouter()
	body, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"access_token": "xai-access-token",
			"expires_at":   time.Now().UTC().Add(-2 * time.Hour).Unix(),
		},
	})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/grok/oauth/normalize", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "expired")
}

func TestGrokOAuthCreateAccount(t *testing.T) {
	router, adminSvc := setupGrokOAuthRouter()
	body, err := json.Marshal(map[string]any{
		"name":                       "Manual Grok",
		"notes":                      "from admin",
		"proxy_id":                   4,
		"concurrency":                7,
		"priority":                   80,
		"rate_multiplier":            1.5,
		"load_factor":                20,
		"group_ids":                  []int64{2, 3},
		"expires_at":                 time.Now().UTC().Add(24 * time.Hour).Unix(),
		"auto_pause_on_expired":      true,
		"skip_default_group_bind":    true,
		"confirm_mixed_channel_risk": true,
		"data": map[string]any{
			"token": map[string]any{
				"access_token":   "xai-access-token",
				"refresh_token":  "xai-refresh-token",
				"id_token":       "xai-id-token",
				"token_type":     "Bearer",
				"expires_in":     3600,
				"scope":          "openid profile email",
				"client_id":      "client-456",
				"token_endpoint": "https://auth.x.ai/oauth/token",
				"base_url":       "https://api.x.ai/v1",
				"email":          "create@example.com",
				"subject":        "subject-456",
				"name":           "create-user",
				"display_name":   "Create User",
			},
		},
	})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/grok/oauth/create-account", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, adminSvc.createdAccounts, 1)
	created := adminSvc.createdAccounts[0]
	require.Equal(t, "Manual Grok", created.Name)
	require.Equal(t, service.PlatformGrok, created.Platform)
	require.Equal(t, service.AccountTypeOAuth, created.Type)
	require.Equal(t, "xai-access-token", created.Credentials["access_token"])
	require.Equal(t, "https://api.x.ai/v1", created.Credentials["api_base_url"])
	require.Equal(t, []string{"openid", "profile", "email"}, created.Credentials["scopes"])
	require.Equal(t, "xai", created.Extra["oauth_provider"])
	require.Equal(t, "Create User", created.Extra["display_name"])
	require.Equal(t, "create@example.com", created.Extra["email"])
	require.Equal(t, "https://api.x.ai/v1", created.Extra["api_base_url"])
	require.Equal(t, true, created.SkipDefaultGroupBind)
	require.Equal(t, true, created.SkipMixedChannelCheck)
	require.Equal(t, 7, created.Concurrency)
	require.Equal(t, 80, created.Priority)
}
