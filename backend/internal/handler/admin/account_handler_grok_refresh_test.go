//go:build unit

package admin

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type grokRefreshOAuthStub struct {
	account      *service.Account
	info         *service.GrokTokenInfo
	ssoInfo      *service.GrokTokenInfo
	refreshErr   error
	ssoErr       error
	calls        int
	ssoCalls     int
	lastSSOToken string
	lastProxyID  *int64
}

func (s *grokRefreshOAuthStub) RefreshAccountToken(_ context.Context, account *service.Account) (*service.GrokTokenInfo, error) {
	s.calls++
	s.account = account
	if s.refreshErr != nil {
		return nil, s.refreshErr
	}
	return s.info, nil
}

func (s *grokRefreshOAuthStub) BuildAccountCredentials(info *service.GrokTokenInfo) map[string]any {
	return map[string]any{
		"access_token":  info.AccessToken,
		"refresh_token": info.RefreshToken,
		"expires_at":    info.ExpiresAt,
		"base_url":      "https://api.x.ai/v1",
	}
}

func (s *grokRefreshOAuthStub) ConvertFromSSO(_ context.Context, ssoToken string, proxyID *int64) (*service.GrokTokenInfo, error) {
	s.ssoCalls++
	s.lastSSOToken = ssoToken
	s.lastProxyID = proxyID
	if s.ssoErr != nil {
		return nil, s.ssoErr
	}
	return s.ssoInfo, nil
}

type grokRefreshAdminService struct {
	*stubAdminService
	updatedCredentials map[string]any
	clearErrorCalls    int
	clearedID          int64
	// lastLoaded mirrors production UpdateAccount: re-read account with groups intact.
	lastLoaded *service.Account
}

func (s *grokRefreshAdminService) UpdateAccount(_ context.Context, id int64, input *service.UpdateAccountInput) (*service.Account, error) {
	s.updatedCredentials = input.Credentials
	// Production UpdateAccount only rebinds when input.GroupIDs != nil; credentials-only
	// refresh must keep existing group bindings on the reloaded account.
	out := &service.Account{
		ID:          id,
		Platform:    service.PlatformGrok,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Credentials: input.Credentials,
	}
	if s.lastLoaded != nil {
		out.GroupIDs = append([]int64(nil), s.lastLoaded.GroupIDs...)
		out.Groups = s.lastLoaded.Groups
		out.Extra = s.lastLoaded.Extra
		out.ProxyID = s.lastLoaded.ProxyID
		if s.lastLoaded.Status != "" {
			// status stays as stored until ClearAccountError; credentials update alone does not clear error.
			out.Status = s.lastLoaded.Status
		}
	}
	if input.GroupIDs != nil {
		out.GroupIDs = append([]int64(nil), (*input.GroupIDs)...)
	}
	return out, nil
}

func (s *grokRefreshAdminService) ClearAccountError(_ context.Context, id int64) (*service.Account, error) {
	s.clearErrorCalls++
	s.clearedID = id
	out := &service.Account{
		ID:          id,
		Platform:    service.PlatformGrok,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Credentials: s.updatedCredentials,
	}
	if s.lastLoaded != nil {
		out.GroupIDs = append([]int64(nil), s.lastLoaded.GroupIDs...)
		out.Groups = s.lastLoaded.Groups
		out.Extra = s.lastLoaded.Extra
		out.ProxyID = s.lastLoaded.ProxyID
	}
	return out, nil
}

func newGrokRefreshHandler(adminSvc service.AdminService, grokOAuth service.GrokOAuthTokenService) *AccountHandler {
	return NewAccountHandler(
		adminSvc,
		nil,
		nil,
		nil,
		nil,
		grokOAuth,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
}

func TestRefreshSingleAccountRoutesGrokThroughGrokOAuthService(t *testing.T) {
	t.Parallel()

	account := &service.Account{
		ID:       4227,
		Platform: service.PlatformGrok,
		Type:     service.AccountTypeOAuth,
		GroupIDs: []int64{2},
		Groups:   []*service.Group{{ID: 2, Name: "g2"}},
		Credentials: map[string]any{
			"access_token":       "old-access",
			"refresh_token":      "old-refresh",
			"base_url":           "https://example.invalid/v1",
			"subscription_tier":  "SUPER_GROK",
			"entitlement_status": "ACTIVE",
		},
	}
	adminSvc := &grokRefreshAdminService{
		stubAdminService: newStubAdminService(),
		lastLoaded:       account,
	}
	grokOAuth := &grokRefreshOAuthStub{info: &service.GrokTokenInfo{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		ExpiresAt:    1_800_000_000,
	}}
	handler := newGrokRefreshHandler(adminSvc, grokOAuth)

	updated, warning, err := handler.refreshSingleAccount(context.Background(), account)
	require.NoError(t, err)
	require.Empty(t, warning)
	require.Equal(t, 1, grokOAuth.calls)
	require.Zero(t, grokOAuth.ssoCalls)
	require.Zero(t, adminSvc.clearErrorCalls)
	require.Same(t, account, grokOAuth.account)
	require.Equal(t, "new-access", adminSvc.updatedCredentials["access_token"])
	require.Equal(t, "new-refresh", adminSvc.updatedCredentials["refresh_token"])
	require.Equal(t, "https://example.invalid/v1", adminSvc.updatedCredentials["base_url"])
	require.Equal(t, "SUPER_GROK", adminSvc.updatedCredentials["subscription_tier"])
	require.Equal(t, "ACTIVE", adminSvc.updatedCredentials["entitlement_status"])
	require.Equal(t, adminSvc.updatedCredentials, updated.Credentials)
	require.Equal(t, []int64{2}, updated.GroupIDs)
	require.Len(t, updated.Groups, 1)
}

func TestRefreshSingleAccountGrokFallsBackToSSOAndClearsError(t *testing.T) {
	t.Parallel()

	proxyID := int64(5)
	group := &service.Group{ID: 11, Name: "grok801"}
	account := &service.Account{
		ID:       1949,
		Platform: service.PlatformGrok,
		Type:     service.AccountTypeOAuth,
		Status:   service.StatusError,
		ProxyID:  &proxyID,
		GroupIDs: []int64{11},
		Groups:   []*service.Group{group},
		Credentials: map[string]any{
			"access_token":  "old-access",
			"refresh_token": "revoked-refresh",
			"base_url":      "https://example.invalid/v1",
		},
		Extra: map[string]any{
			"sso": "sso-session-token",
		},
	}
	adminSvc := &grokRefreshAdminService{
		stubAdminService: newStubAdminService(),
		lastLoaded:       account,
	}
	grokOAuth := &grokRefreshOAuthStub{
		refreshErr: errors.New("refresh token revoked"),
		ssoInfo: &service.GrokTokenInfo{
			AccessToken:  "sso-access",
			RefreshToken: "sso-refresh",
			ExpiresAt:    1_900_000_000,
		},
	}
	handler := newGrokRefreshHandler(adminSvc, grokOAuth)

	updated, warning, err := handler.refreshSingleAccount(context.Background(), account)
	require.NoError(t, err)
	require.Empty(t, warning)
	require.Equal(t, 1, grokOAuth.calls)
	require.Equal(t, 1, grokOAuth.ssoCalls)
	require.Equal(t, "sso-session-token", grokOAuth.lastSSOToken)
	require.NotNil(t, grokOAuth.lastProxyID)
	require.Equal(t, proxyID, *grokOAuth.lastProxyID)
	require.Equal(t, 1, adminSvc.clearErrorCalls)
	require.Equal(t, int64(1949), adminSvc.clearedID)
	require.Equal(t, "sso-access", adminSvc.updatedCredentials["access_token"])
	require.Equal(t, "sso-refresh", adminSvc.updatedCredentials["refresh_token"])
	require.Equal(t, "https://example.invalid/v1", adminSvc.updatedCredentials["base_url"])
	require.Equal(t, service.StatusActive, updated.Status)
	require.Equal(t, adminSvc.updatedCredentials, updated.Credentials)
	// credentials-only refresh must not clear group bindings
	require.Equal(t, []int64{11}, updated.GroupIDs)
	require.Len(t, updated.Groups, 1)
	require.Equal(t, int64(11), updated.Groups[0].ID)
}

func TestRefreshSingleAccountGrokRefreshFailureWithoutSSO(t *testing.T) {
	t.Parallel()

	adminSvc := &grokRefreshAdminService{stubAdminService: newStubAdminService()}
	grokOAuth := &grokRefreshOAuthStub{
		refreshErr: errors.New("refresh token revoked"),
	}
	handler := newGrokRefreshHandler(adminSvc, grokOAuth)
	account := &service.Account{
		ID:       1950,
		Platform: service.PlatformGrok,
		Type:     service.AccountTypeOAuth,
		Status:   service.StatusError,
		Credentials: map[string]any{
			"refresh_token": "revoked-refresh",
		},
	}

	updated, warning, err := handler.refreshSingleAccount(context.Background(), account)
	require.Error(t, err)
	require.Nil(t, updated)
	require.Empty(t, warning)
	require.Contains(t, err.Error(), "refresh token revoked")
	require.Equal(t, 1, grokOAuth.calls)
	require.Zero(t, grokOAuth.ssoCalls)
	require.Zero(t, adminSvc.clearErrorCalls)
	require.Nil(t, adminSvc.updatedCredentials)
}

func TestRefreshSingleAccountGrokSSOFallbackAlsoFails(t *testing.T) {
	t.Parallel()

	adminSvc := &grokRefreshAdminService{stubAdminService: newStubAdminService()}
	grokOAuth := &grokRefreshOAuthStub{
		refreshErr: errors.New("refresh token revoked"),
		ssoErr:     errors.New("sso convert failed"),
	}
	handler := newGrokRefreshHandler(adminSvc, grokOAuth)
	account := &service.Account{
		ID:       1951,
		Platform: service.PlatformGrok,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "revoked-refresh",
		},
		Extra: map[string]any{
			"sso": "bad-sso",
		},
	}

	updated, warning, err := handler.refreshSingleAccount(context.Background(), account)
	require.Error(t, err)
	require.Nil(t, updated)
	require.Empty(t, warning)
	require.Contains(t, err.Error(), "refresh token revoked")
	require.Contains(t, err.Error(), "sso fallback failed")
	require.Equal(t, 1, grokOAuth.calls)
	require.Equal(t, 1, grokOAuth.ssoCalls)
	require.Zero(t, adminSvc.clearErrorCalls)
}
