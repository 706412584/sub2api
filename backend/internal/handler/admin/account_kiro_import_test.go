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
