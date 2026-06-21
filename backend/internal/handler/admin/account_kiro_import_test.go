package admin

import (
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
