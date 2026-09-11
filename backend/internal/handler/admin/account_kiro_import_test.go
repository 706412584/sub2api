package admin

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestParseKiroImportDataSupportsAPIKey(t *testing.T) {
	accounts, err := parseKiroImportData(map[string]any{
		"name":       "cli-key",
		"kiroApiKey": "ksk_test-value",
		"endpoint":   "cli",
		"authRegion": "eu-west-1",
		"api_region": "us-west-2",
	})
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, "ksk_test-value", accounts[0].KiroAPIKey)
	require.Equal(t, "api_key", accounts[0].AuthMethod)
	require.Equal(t, "cli", accounts[0].Endpoint)
	require.Equal(t, "eu-west-1", accounts[0].AuthRegion)
	require.Equal(t, "us-west-2", accounts[0].APIRegion)
}

func TestKiroImportAPIKeyCreatesAPIKeyAccount(t *testing.T) {
	stub := newStubAdminService()
	upstream := &syncUpstreamHTTPUpstream{resp: &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"models":[{"modelId":"claude-sonnet-5"},{"modelId":"gpt-5.6-sol"}]}`)),
	}}
	accountTestService := service.NewAccountTestService(
		nil, nil, nil, nil, nil, upstream,
		&config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		nil,
	)
	h := &AccountHandler{adminService: stub, accountTestService: accountTestService}
	accounts, err := parseKiroImportData(map[string]any{
		"name":         "cli-key",
		"kiro_api_key": "ksk_test-value",
	})
	require.NoError(t, err)

	result, err := h.importKiroAccounts(t.Context(), KiroImportRequest{}, accounts)
	require.NoError(t, err)
	require.Equal(t, 1, result.Created)
	require.Len(t, stub.createdAccounts, 1)
	created := stub.createdAccounts[0]
	require.Equal(t, service.PlatformKiro, created.Platform)
	require.Equal(t, service.AccountTypeAPIKey, created.Type)
	require.Equal(t, "ksk_test-value", created.Credentials["kiro_api_key"])
	require.Equal(t, "api_key", created.Credentials["auth_method"])
	require.Equal(t, "cli", created.Credentials["endpoint"])
	require.Equal(t, map[string]any{
		"claude-sonnet-5": "claude-sonnet-5",
		"gpt-5.6-sol":     "gpt-5.6-sol",
	}, created.Credentials["model_mapping"])
	require.NotContains(t, created.Credentials, "refresh_token")
}

func TestParseKiroImportDataRejectsMalformedAPIKey(t *testing.T) {
	_, err := parseKiroImportData(map[string]any{"kiro_api_key": "sk_wrong"})
	require.Error(t, err)
}

func TestParseKiroImportDataNormalizesMillisecondExpiresAt(t *testing.T) {
	// Kiro Account Manager exports credentials.expiresAt in milliseconds.
	accounts, err := parseKiroImportData(map[string]any{
		"email": "user@example.com",
		"credentials": map[string]any{
			"accessToken":  "aoa-test",
			"refreshToken": "aor-test",
			"expiresAt":    int64(1781595765599),
			"authMethod":   "social",
			"provider":     "Google",
			"region":       "us-east-1",
		},
	})
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, int64(1781595765), accounts[0].ExpiresAt)
	require.Equal(t, "social", accounts[0].AuthMethod)
	require.Equal(t, "aor-test", accounts[0].RefreshToken)
}

func TestImportKiroAccountsStoresSecondExpiresAt(t *testing.T) {
	stub := newStubAdminService()
	h := &AccountHandler{adminService: stub}
	accounts, err := parseKiroImportData(map[string]any{
		"name": "kiro-social",
		"credentials": map[string]any{
			"accessToken":  "aoa-test",
			"refreshToken": "aor-test",
			"expiresAt":    1781595765599,
			"authMethod":   "social",
		},
	})
	require.NoError(t, err)

	result, err := h.importKiroAccounts(t.Context(), KiroImportRequest{}, accounts)
	require.NoError(t, err)
	require.Equal(t, 1, result.Created)
	require.Len(t, stub.createdAccounts, 1)
	created := stub.createdAccounts[0]
	require.Equal(t, service.AccountTypeOAuth, created.Type)
	require.Equal(t, int64(1781595765), created.Credentials["expires_at"])
	require.Equal(t, "aor-test", created.Credentials["refresh_token"])
}
