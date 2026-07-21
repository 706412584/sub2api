package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCreateKiroAPIKeyAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := newStubAdminService()
	h := NewKiroOAuthHandler(stub, nil)
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
	require.NotContains(t, recorder.Body.String(), "ksk_test-value")
}

func TestCreateKiroAPIKeyAccountRejectsMalformedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := newStubAdminService()
	h := NewKiroOAuthHandler(stub, nil)
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
