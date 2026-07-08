package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGrokOAuthServiceGenerateAuthURLStoresSessionAndState(t *testing.T) {
	svc := NewGrokOAuthService(nil)
	defer svc.Stop()

	result, err := svc.GenerateAuthURL(context.Background(), nil, "http://localhost:3000/auth/callback")
	require.NoError(t, err)
	require.NotEmpty(t, result.AuthURL)
	require.NotEmpty(t, result.SessionID)
	require.NotEmpty(t, result.State)

	parsed, err := url.Parse(result.AuthURL)
	require.NoError(t, err)
	query := parsed.Query()
	require.Equal(t, GrokOAuthClientID, query.Get("client_id"))
	require.Equal(t, result.State, query.Get("state"))
	require.Equal(t, GrokOAuthScope, query.Get("scope"))

	session, ok := svc.sessionStore.Get(result.SessionID)
	require.True(t, ok)
	require.Equal(t, result.State, session.State)
	require.Equal(t, GrokOAuthClientID, session.ClientID)
	require.Equal(t, "http://localhost:3000/auth/callback", session.RedirectURI)
	require.NotEmpty(t, session.CodeVerifier)
}

func TestGrokOAuthServiceBuildAccountCredentialsPreservesExpectedFields(t *testing.T) {
	svc := NewGrokOAuthService(nil)
	creds := svc.BuildAccountCredentials(&GrokTokenInfo{
		AccessToken:   "access-token",
		RefreshToken:  "refresh-token",
		IDToken:       "id-token",
		TokenType:     "Bearer",
		ClientID:      GrokOAuthClientID,
		TokenEndpoint: GrokOAuthTokenEndpoint,
		APIBaseURL:    GrokOAuthAPIBaseURL,
		ExpiresAt:     1_750_000_000,
		Scope:         "openid profile email",
		Scopes:        []string{"openid", "profile", "email"},
	})

	require.Equal(t, "access-token", creds["access_token"])
	require.Equal(t, "refresh-token", creds["refresh_token"])
	require.Equal(t, "id-token", creds["id_token"])
	require.Equal(t, "Bearer", creds["token_type"])
	require.Equal(t, GrokOAuthClientID, creds["client_id"])
	require.Equal(t, GrokOAuthTokenEndpoint, creds["token_endpoint"])
	require.Equal(t, GrokOAuthAPIBaseURL, creds["api_base_url"])
	require.Equal(t, int64(1_750_000_000), creds["expires_at"])
	require.Equal(t, "openid profile email", creds["scope"])
	require.Equal(t, []string{"openid", "profile", "email"}, creds["scopes"])
}

func TestGrokOAuthServiceRefreshAccountTokenPreservesOldRefreshToken(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "refresh_token", r.PostForm.Get("grant_type"))
		require.Equal(t, "old-refresh", r.PostForm.Get("refresh_token"))
		require.Equal(t, GrokOAuthClientID, r.PostForm.Get("client_id"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","token_type":"Bearer","expires_in":1800,"scope":"openid profile"}`))
	}))
	defer tokenServer.Close()

	svc := NewGrokOAuthService(nil)
	svc.httpClient = tokenServer.Client()
	svc.tokenEndpoint = tokenServer.URL

	account := &Account{
		ID:       9,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "old-refresh",
			"expires_at":    time.Now().Add(-time.Minute).Unix(),
		},
	}

	info, err := svc.RefreshAccountToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "new-access", info.AccessToken)
	require.Equal(t, "old-refresh", info.RefreshToken)
	require.Equal(t, "openid profile", info.Scope)
}
