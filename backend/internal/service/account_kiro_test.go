package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKiroAccountCredentialHelpers(t *testing.T) {
	oauth := &Account{
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "oauth-token",
			"endpoint":     "ide",
			"auth_region":  "eu-west-1",
			"api_region":   "us-west-2",
		},
	}
	require.True(t, oauth.IsKiro())
	require.True(t, oauth.IsKiroOAuth())
	require.False(t, oauth.IsKiroAPIKey())
	require.Equal(t, "oauth-token", oauth.GetKiroToken())
	require.Equal(t, "ide", oauth.GetKiroEndpoint())
	require.Equal(t, "eu-west-1", oauth.GetKiroAuthRegion())
	require.Equal(t, "us-west-2", oauth.GetKiroAPIRegion())

	apiKey := &Account{
		Platform: PlatformKiro,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"kiro_api_key": "ksk_test-value",
		},
	}
	require.True(t, apiKey.IsKiroAPIKey())
	require.False(t, apiKey.IsKiroOAuth())
	require.Equal(t, "ksk_test-value", apiKey.GetKiroToken())
	require.Equal(t, "cli", apiKey.GetKiroEndpoint())
	require.Equal(t, "us-east-1", apiKey.GetKiroAuthRegion())
	require.Equal(t, "us-east-1", apiKey.GetKiroAPIRegion())
}

func TestValidateKiroAccountCredentials(t *testing.T) {
	tests := []struct {
		name        string
		accountType string
		credentials map[string]any
		wantErr     bool
	}{
		{name: "oauth unchanged", accountType: AccountTypeOAuth, credentials: map[string]any{"refresh_token": "rt"}},
		{name: "api key valid", accountType: AccountTypeAPIKey, credentials: map[string]any{"kiro_api_key": "ksk_secret", "endpoint": "cli"}},
		{name: "api key ide rejected", accountType: AccountTypeAPIKey, credentials: map[string]any{"kiro_api_key": "ksk_secret", "endpoint": "ide"}, wantErr: true},
		{name: "api key missing", accountType: AccountTypeAPIKey, credentials: map[string]any{}, wantErr: true},
		{name: "api key wrong prefix", accountType: AccountTypeAPIKey, credentials: map[string]any{"kiro_api_key": "sk_wrong"}, wantErr: true},
		{name: "api key prefix only", accountType: AccountTypeAPIKey, credentials: map[string]any{"kiro_api_key": "ksk_"}, wantErr: true},
		{name: "invalid endpoint", accountType: AccountTypeAPIKey, credentials: map[string]any{"kiro_api_key": "ksk_secret", "endpoint": "other"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateKiroAccountCredentials(PlatformKiro, tt.accountType, tt.credentials)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
