package service

import (
	"testing"
	"time"

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

func TestGetCredentialAsTimeNormalizesMillisecondEpoch(t *testing.T) {
	// Kiro manager and some exports store expiresAt in ms; stored as-is must still refresh.
	account := &Account{
		Credentials: map[string]any{
			"expires_at": int64(1781595765599),
		},
	}
	got := account.GetCredentialAsTime("expires_at")
	require.NotNil(t, got)
	require.Equal(t, int64(1781595765), got.Unix())

	seconds := &Account{Credentials: map[string]any{"expires_at": int64(1781595765)}}
	gotSec := seconds.GetCredentialAsTime("expires_at")
	require.NotNil(t, gotSec)
	require.Equal(t, int64(1781595765), gotSec.Unix())
}

func TestKiroTokenRefresherNeedsRefreshWithMillisecondExpiresAt(t *testing.T) {
	refresher := NewKiroTokenRefresher()
	// Already-expired token stored as ms epoch (past relative to now after normalization)
	pastMs := time.Now().Add(-2 * time.Hour).UnixMilli()
	account := &Account{
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "aoa",
			"refresh_token": "aor",
			"expires_at":    pastMs,
			"auth_method":   "social",
		},
	}
	require.True(t, refresher.CanRefresh(account))
	require.True(t, refresher.NeedsRefresh(account, 30*time.Minute))
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
