//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestTokenRefreshServiceRegistersKiroOAuthOnly(t *testing.T) {
	svc := NewTokenRefreshService(&tokenRefreshAccountRepo{}, nil, nil, nil, nil, nil, nil, &config.Config{}, nil)

	require.Contains(t, svc.eligiblePlatforms(), PlatformKiro)
	var kiro TokenRefresher
	for _, registration := range svc.registrations {
		if registration.platform == PlatformKiro {
			kiro = registration.refresher
			break
		}
	}
	require.NotNil(t, kiro)
	require.True(t, kiro.CanRefresh(&Account{Platform: PlatformKiro, Type: AccountTypeOAuth}))
	require.False(t, kiro.CanRefresh(&Account{Platform: PlatformKiro, Type: AccountTypeAPIKey}))
}
