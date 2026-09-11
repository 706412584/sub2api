//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type grokTokenServiceStub struct {
	info       *GrokTokenInfo
	ssoInfo    *GrokTokenInfo
	refreshErr error
	ssoErr     error
	calls      int
	ssoCalls   int
	lastSSO    string
	lastProxy  *int64
}

func (s *grokTokenServiceStub) RefreshAccountToken(_ context.Context, _ *Account) (*GrokTokenInfo, error) {
	s.calls++
	if s.refreshErr != nil {
		return nil, s.refreshErr
	}
	return s.info, nil
}

func (s *grokTokenServiceStub) BuildAccountCredentials(info *GrokTokenInfo) map[string]any {
	if info == nil {
		return nil
	}
	creds := map[string]any{
		"access_token": info.AccessToken,
		"expires_at":   info.ExpiresAt,
	}
	if info.RefreshToken != "" {
		creds["refresh_token"] = info.RefreshToken
	}
	return creds
}

func (s *grokTokenServiceStub) ConvertFromSSO(_ context.Context, ssoToken string, proxyID *int64) (*GrokTokenInfo, error) {
	s.ssoCalls++
	s.lastSSO = ssoToken
	s.lastProxy = proxyID
	if s.ssoErr != nil {
		return nil, s.ssoErr
	}
	return s.ssoInfo, nil
}

func TestRefreshGrokAccountTokenWithSSOFallbackUsesRefreshWhenOK(t *testing.T) {
	t.Parallel()
	stub := &grokTokenServiceStub{info: &GrokTokenInfo{AccessToken: "a", RefreshToken: "r"}}
	account := &Account{
		ID:       1,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "old-r",
		},
		Extra: map[string]any{"sso": "sso-token"},
	}
	info, err := RefreshGrokAccountTokenWithSSOFallback(context.Background(), stub, account)
	require.NoError(t, err)
	require.Equal(t, "a", info.AccessToken)
	require.Equal(t, 1, stub.calls)
	require.Zero(t, stub.ssoCalls)
}

func TestRefreshGrokAccountTokenWithSSOFallbackConvertsSSO(t *testing.T) {
	t.Parallel()
	proxyID := int64(5)
	stub := &grokTokenServiceStub{
		refreshErr: errors.New("rt revoked"),
		ssoInfo:    &GrokTokenInfo{AccessToken: "sso-a", RefreshToken: "sso-r"},
	}
	account := &Account{
		ID:       2,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		ProxyID:  &proxyID,
		Credentials: map[string]any{
			"refresh_token": "bad-r",
		},
		Extra: map[string]any{"sso": "sso-token"},
	}
	info, err := RefreshGrokAccountTokenWithSSOFallback(context.Background(), stub, account)
	require.NoError(t, err)
	require.Equal(t, "sso-a", info.AccessToken)
	require.Equal(t, "sso-r", info.RefreshToken)
	require.Equal(t, 1, stub.calls)
	require.Equal(t, 1, stub.ssoCalls)
	require.Equal(t, "sso-token", stub.lastSSO)
	require.NotNil(t, stub.lastProxy)
	require.Equal(t, proxyID, *stub.lastProxy)
}

func TestRefreshGrokAccountTokenWithSSOFallbackNoSSOKeepsError(t *testing.T) {
	t.Parallel()
	stub := &grokTokenServiceStub{refreshErr: errors.New("rt revoked")}
	account := &Account{
		ID:       3,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "bad-r",
		},
	}
	info, err := RefreshGrokAccountTokenWithSSOFallback(context.Background(), stub, account)
	require.Error(t, err)
	require.Nil(t, info)
	require.Equal(t, "rt revoked", err.Error())
	require.Zero(t, stub.ssoCalls)
}

func TestGrokTokenRefresherCanRefreshWithSSOOnly(t *testing.T) {
	t.Parallel()
	refresher := NewGrokTokenRefresher(&grokTokenServiceStub{})
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Extra:    map[string]any{"sso": "sso-only"},
	}
	require.True(t, refresher.CanRefresh(account))
	require.True(t, refresher.NeedsRefresh(account, time.Hour))
}

func TestGrokTokenRefresherRefreshFallsBackToSSO(t *testing.T) {
	t.Parallel()
	stub := &grokTokenServiceStub{
		refreshErr: errors.New("rt revoked"),
		ssoInfo:    &GrokTokenInfo{AccessToken: "sso-a", RefreshToken: "sso-r", ExpiresAt: 123},
	}
	refresher := NewGrokTokenRefresher(stub)
	account := &Account{
		ID:       9,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "bad-r",
			"base_url":      "https://custom.example/v1",
		},
		Extra: map[string]any{"sso": "sso-token"},
	}
	creds, err := refresher.Refresh(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "sso-a", creds["access_token"])
	require.Equal(t, "sso-r", creds["refresh_token"])
	require.Equal(t, "https://custom.example/v1", creds["base_url"])
	require.Equal(t, 1, stub.ssoCalls)
}
