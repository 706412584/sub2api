package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGrokTokenRefresherCanRefresh(t *testing.T) {
	refresher := NewGrokTokenRefresher(nil)
	require.True(t, refresher.CanRefresh(&Account{Platform: PlatformGrok, Type: AccountTypeOAuth}))
	require.False(t, refresher.CanRefresh(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	require.False(t, refresher.CanRefresh(&Account{Platform: PlatformGrok, Type: AccountTypeAPIKey}))
}

func TestGrokTokenRefresherNeedsRefresh(t *testing.T) {
	refresher := NewGrokTokenRefresher(nil)
	window := 10 * time.Minute

	require.False(t, refresher.NeedsRefresh(&Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"expires_at":    time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
			"refresh_token": "refresh-token",
		},
	}, window))

	require.True(t, refresher.NeedsRefresh(&Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"expires_at":    time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
			"refresh_token": "refresh-token",
		},
	}, window))

	require.True(t, refresher.NeedsRefresh(&Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "refresh-token",
		},
	}, window))

	require.False(t, refresher.NeedsRefresh(&Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"expires_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
		},
	}, window))
}
