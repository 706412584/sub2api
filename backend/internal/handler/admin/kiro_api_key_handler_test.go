package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCreateKiroAPIKeyAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := newStubAdminService()
	upstream := &syncUpstreamHTTPUpstream{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"models":[{"modelId":"claude-sonnet-5"},{"modelId":"gpt-5.6-sol"}]}`)),
	}}
	accountTestService := service.NewAccountTestService(
		nil, nil, nil, nil, nil, upstream,
		&config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		nil,
	)
	h := NewKiroOAuthHandler(stub, nil, accountTestService, nil)
	body, err := json.Marshal(map[string]any{
		"name":         "kiro-key",
		"kiro_api_key": "ksk_test-value",
	})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/admin/kiro/api-key/create-account", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.CreateAPIKeyAccount(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Len(t, stub.createdAccounts, 1)
	created := stub.createdAccounts[0]
	require.Equal(t, service.PlatformKiro, created.Platform)
	require.Equal(t, service.AccountTypeAPIKey, created.Type)
	require.Equal(t, "ksk_test-value", created.Credentials["kiro_api_key"])
	require.Equal(t, "cli", created.Credentials["endpoint"])
	require.Equal(t, "us-east-1", created.Credentials["auth_region"])
	require.Equal(t, "us-east-1", created.Credentials["api_region"])
	require.Equal(t, map[string]any{
		"claude-sonnet-5": "claude-sonnet-5",
		"gpt-5.6-sol":     "gpt-5.6-sol",
	}, created.Credentials["model_mapping"])
	require.NotContains(t, recorder.Body.String(), "ksk_test-value")
}

func TestCreateKiroAPIKeyAccountRejectsMalformedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := newStubAdminService()
	h := NewKiroOAuthHandler(stub, nil, nil, nil)
	body := []byte(`{"name":"kiro-key","kiro_api_key":"sk_wrong"}`)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/admin/kiro/api-key/create-account", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.CreateAPIKeyAccount(ctx)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Empty(t, stub.createdAccounts)
}

func TestRefreshSingleAccountRejectsKiroAPIKey(t *testing.T) {
	h := &AccountHandler{}
	_, _, err := h.refreshSingleAccount(t.Context(), &service.Account{
		Platform: service.PlatformKiro,
		Type:     service.AccountTypeAPIKey,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be refreshed")
}
