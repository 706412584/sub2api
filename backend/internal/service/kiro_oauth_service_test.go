package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKiroTokenRefresherRefreshesExternalIDPAccount(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "client-id", r.PostForm.Get("client_id"))
		require.Equal(t, "refresh_token", r.PostForm.Get("grant_type"))
		require.Equal(t, "refresh-old", r.PostForm.Get("refresh_token"))
		require.Equal(t, "openid profile", r.PostForm.Get("scope"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access-new","refresh_token":"refresh-new","expires_in":1800}`))
	}))
	defer tokenServer.Close()

	account := &Account{
		ID:       12,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token":  "refresh-old",
			"client_id":      "client-id",
			"auth_method":    "external_idp",
			"token_endpoint": tokenServer.URL,
			"scope":          "openid profile",
		},
	}

	refresher := NewKiroTokenRefresher()
	require.True(t, refresher.CanRefresh(account))
	require.True(t, refresher.NeedsRefresh(account, time.Hour))
	credentials, err := refresher.Refresh(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "access-new", credentials["access_token"])
	require.Equal(t, "refresh-new", credentials["refresh_token"])
	require.Equal(t, int64(1800), credentials["expires_in"])
	require.NotEmpty(t, credentials["expires_at"])
}
