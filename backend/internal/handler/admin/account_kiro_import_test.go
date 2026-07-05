package admin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseKiroImportDataSupportsCamelCaseAndNestedCredentials(t *testing.T) {
	input := map[string]any{
		"version": "merged",
		"accounts": []any{
			map[string]any{
				"name": "work",
				"credentials": map[string]any{
					"accessToken":   "at",
					"refreshToken":  "rt",
					"clientId":      "cid",
					"clientSecret":  "secret",
					"authMethod":    "builderId",
					"region":        "eu-west-1",
					"profileArn":    "arn:aws:codewhisperer:eu-west-1:123:profile/profile-id",
					"tokenEndpoint": "https://idp.example/token",
					"issuerUrl":     "https://idp.example/",
					"scopes":        []any{"openid", "profile"},
					"startUrl":      "https://example.awsapps.com/start",
				},
			},
		},
	}

	accounts, err := parseKiroImportData(input)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, "work", accounts[0].Name)
	require.Equal(t, "at", accounts[0].AccessToken)
	require.Equal(t, "rt", accounts[0].RefreshToken)
	require.Equal(t, "cid", accounts[0].ClientID)
	require.Equal(t, "secret", accounts[0].ClientSecret)
	require.Equal(t, "idc", accounts[0].AuthMethod)
	require.Equal(t, "eu-west-1", accounts[0].Region)
	require.Equal(t, "arn:aws:codewhisperer:eu-west-1:123:profile/profile-id", accounts[0].ProfileArn)
	require.Equal(t, "https://idp.example/token", accounts[0].TokenEndpoint)
	require.Equal(t, []string{"openid", "profile"}, accounts[0].Scopes)
	require.Equal(t, "https://example.awsapps.com/start", accounts[0].StartURL)
}

func TestParseKiroImportDataSupportsExternalIDPEnvelope(t *testing.T) {
	input := map[string]any{
		"external_idp": map[string]any{
			"refreshToken":  "rt-external",
			"clientId":      "external-client",
			"tokenEndpoint": "https://login.example/token",
			"scopes":        "openid profile offline_access",
		},
		"profileArn": "arn:aws:codewhisperer:us-east-1:123:profile/external",
	}

	accounts, err := parseKiroImportData(input)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, "rt-external", accounts[0].RefreshToken)
	require.Equal(t, "external-client", accounts[0].ClientID)
	require.Equal(t, "external_idp", accounts[0].AuthMethod)
	require.Equal(t, "ExternalIdp", accounts[0].Provider)
	require.Equal(t, []string{"openid", "profile", "offline_access"}, accounts[0].Scopes)
	require.NotNil(t, accounts[0].ExternalIDP)
}

func TestParseKiroImportDataSupportsSingleAccountFixedKeys(t *testing.T) {
	input := map[string]any{
		"account": map[string]any{
			"clientId":     "cid",
			"clientSecret": "secret",
			"email":        "alexander@example.com",
			"platform":     "kiro",
			"provider":     "BuilderId",
			"refreshToken": "rt",
			"region":       "us-east-1",
			"machineId":    "abcdef123456",
			"subscription": map[string]any{
				"type": "Pro+",
			},
		},
	}

	accounts, err := parseKiroImportData(input)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, "alexander@example.com", accounts[0].Email)
	require.Equal(t, "cid", accounts[0].ClientID)
	require.Equal(t, "secret", accounts[0].ClientSecret)
	require.Equal(t, "rt", accounts[0].RefreshToken)
	require.Equal(t, "BuilderId", accounts[0].Provider)
	require.Equal(t, "idc", accounts[0].AuthMethod)
	require.Equal(t, "abcdef123456", accounts[0].MachineID)
	require.Equal(t, "Pro+", accounts[0].SubscriptionType)
}

func TestParseKiroImportDataPreservesSubscriptionAndUsageMetadata(t *testing.T) {
	input := map[string]any{
		"accounts": []any{
			map[string]any{
				"credentials": map[string]any{
					"accessToken":  "at",
					"refreshToken": "rt",
				},
				"subscription": map[string]any{
					"type":              "Pro",
					"title":             "KIRO PRO",
					"rawType":           "Q_DEVELOPER_STANDALONE_PRO",
					"daysRemaining":     float64(10),
					"expiresAt":         float64(1782864000000),
					"overageCapability": "OVERAGE_CAPABLE",
				},
				"usage": map[string]any{
					"current":       11003.88,
					"limit":         float64(1000),
					"percentUsed":   11.00388,
					"baseLimit":     float64(1000),
					"nextResetDate": "2026-07-01T00:00:00.000Z",
					"resourceDetail": map[string]any{
						"overageEnabled": true,
						"overageCap":     float64(10000),
						"overageRate":    0.04,
					},
				},
			},
		},
	}

	accounts, err := parseKiroImportData(input)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, "Pro", accounts[0].SubscriptionType)
	require.Equal(t, "KIRO PRO", accounts[0].SubscriptionTitle)
	require.Equal(t, "Q_DEVELOPER_STANDALONE_PRO", accounts[0].SubscriptionRawType)
	require.Equal(t, int64(10), accounts[0].SubscriptionDaysRemaining)
	require.Equal(t, int64(1782864000000), accounts[0].SubscriptionExpiresAt)
	require.Equal(t, "OVERAGE_CAPABLE", accounts[0].SubscriptionOverageCapability)
	require.Equal(t, 11003.88, accounts[0].UsageCurrent)
	require.Equal(t, float64(1000), accounts[0].UsageLimit)
	require.Equal(t, 11.00388, accounts[0].UsagePercentUsed)
	require.Equal(t, "2026-07-01T00:00:00.000Z", accounts[0].UsageNextResetDate)
	require.True(t, accounts[0].UsageOverageEnabled)
	require.Equal(t, float64(10000), accounts[0].UsageOverageCap)
	require.Equal(t, 0.04, accounts[0].UsageOverageRate)
}

// TestParseKiroImportDataSupportsSingleBuilderIDExportFormat covers the export
// shape produced by Amazon Q "single account" downloads — a flat object with
// camelCase top-level keys (clientId / clientSecret / refreshToken / region /
// subscription) and no accessToken or profileArn. inferKiroAuthMethod must
// classify it as IdC because clientId+clientSecret are present.
func TestParseKiroImportDataSupportsSingleBuilderIDExportFormat(t *testing.T) {
	raw := `{
		"clientId": "-LgPyFDkEdrBPI5oQvuph3VzLWVhc3QtMQ",
		"clientSecret": "eyJraWQiOiJrZXktMTU2NDAyODA5OSJ9.payload.sig",
		"email": "alex@example.com",
		"platform": "kiro",
		"provider": "BuilderId",
		"refreshToken": "aorAAAAAGqF7c48y5esjdSqo1hsfKHiUhdg4ijlNBGfiP8caBijvifca6VLpIbE0",
		"region": "us-east-1",
		"subscription": {"type": "Pro"}
	}`
	var input any
	require.NoError(t, json.Unmarshal([]byte(raw), &input))

	accounts, err := parseKiroImportData(input)
	require.NoError(t, err)
	require.Len(t, accounts, 1)

	got := accounts[0]
	require.Equal(t, "-LgPyFDkEdrBPI5oQvuph3VzLWVhc3QtMQ", got.ClientID)
	require.NotEmpty(t, got.ClientSecret)
	require.Equal(t, "alex@example.com", got.Email)
	require.Equal(t, "BuilderId", got.Provider)
	require.Equal(t, "us-east-1", got.Region)
	require.NotEmpty(t, got.RefreshToken)
	require.Equal(t, "idc", got.AuthMethod)
	require.Equal(t, "Pro", got.SubscriptionType)
	require.Empty(t, got.ProfileArn, "BuilderID single export carries no profileArn; downstream must fallback")
	require.Empty(t, got.AccessToken, "single export carries refresh token only")
}

// TestParseKiroImportDataSupportsKiroAccountManagerArrayFormat covers the
// shape produced by the desktop Kiro Account Manager "export all" — a top
// level JSON array where each entry already includes authMethod, accessToken,
// refreshToken, clientId/clientSecret, region, machineId and a nested
// usageData payload. Asserts both that parsing yields the right count and
// that BuildKiroAccountCredentials-style fields land in the expected places.
func TestParseKiroImportDataSupportsKiroAccountManagerArrayFormat(t *testing.T) {
	raw := `[
		{
			"id": "a494e755-938e-41b7-bf0e-04ea060e01c0",
			"email": "coleman@example.com",
			"label": "Kiro BuilderId",
			"status": "active",
			"accessToken": "aoaAAAAAGo5QvADMmKn2dM4O6MbPrOpNer",
			"refreshToken": "aorAAAAAGqF6bo62idU9ZT2D8sMx-3BfDxZ",
			"provider": "BuilderId",
			"authMethod": "IdC",
			"clientId": "EBrw-2er0EUFGXDcjvmPQXVzLWVhc3QtMQ",
			"clientSecret": "eyJraWQiOiJrZXktMTU2NDAyODA5OSJ9.payload.sig",
			"region": "us-east-1",
			"machineId": "fb6214ac-11ef-40cb-b4a5-004e0bdf54a2",
			"profileArn": null,
			"usageData": {
				"subscriptionInfo": {"subscriptionTitle": "KIRO PRO", "type": "Q_DEVELOPER_STANDALONE_PRO"}
			}
		}
	]`
	var input any
	require.NoError(t, json.Unmarshal([]byte(raw), &input))

	accounts, err := parseKiroImportData(input)
	require.NoError(t, err)
	require.Len(t, accounts, 1)

	got := accounts[0]
	require.Equal(t, "coleman@example.com", got.Email)
	require.Equal(t, "EBrw-2er0EUFGXDcjvmPQXVzLWVhc3QtMQ", got.ClientID)
	require.NotEmpty(t, got.AccessToken)
	require.NotEmpty(t, got.RefreshToken)
	require.Equal(t, "us-east-1", got.Region)
	require.Equal(t, "fb6214ac-11ef-40cb-b4a5-004e0bdf54a2", got.MachineID)
	require.Equal(t, "idc", got.AuthMethod, "explicit authMethod=IdC must be normalized to idc")
	require.Empty(t, got.ProfileArn, "JSON null profileArn must not be coerced into a string")
}
