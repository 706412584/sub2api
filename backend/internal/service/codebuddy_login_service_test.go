//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func newCodebuddyLoginTestService(handler http.Handler) *CodebuddyLoginService {
	svc := NewCodebuddyLoginService()
	svc.httpClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec.Result(), nil
	})}
	return svc
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func codebuddyEnvelopeJSON(code int, msg string, data any) []byte {
	raw, _ := json.Marshal(data)
	out := map[string]any{"code": code, "msg": msg, "data": json.RawMessage(raw)}
	body, _ := json.Marshal(out)
	return body
}

// TestCodebuddyLoginStartPollFlow 走完整 start → pending → authorized 链路。
func TestCodebuddyLoginStartPollFlow(t *testing.T) {
	pending := true
	server := newCodebuddyLoginTestService(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v2/plugin/auth/state":
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{"state": "st-1", "authUrl": "https://example.com/auth"}))
		case r.URL.Path == "/v2/plugin/auth/token":
			if pending {
				_, _ = w.Write(codebuddyEnvelopeJSON(1004, "login ing", nil))
				return
			}
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{
				"accessToken": "at-1", "refreshToken": "rt-1", "expiresIn": 3600, "domain": "www.codebuddy.cn",
			}))
		case r.URL.Path == "/v2/plugin/login/account":
			require.Equal(t, "Bearer at-1", r.Header.Get("Authorization"))
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{"uid": "u-1", "enterpriseId": "e-1", "nickname": "nick-1"}))
		default:
			http.NotFound(w, r)
		}
	}))

	start, err := server.Start(context.Background(), "cn")
	require.NoError(t, err)
	require.Equal(t, "cn", start.Region)
	require.NotEmpty(t, start.SessionID)
	require.Equal(t, "https://example.com/auth", start.AuthURL)

	// 授权前：pending
	poll, err := server.Poll(context.Background(), start.SessionID)
	require.NoError(t, err)
	require.Equal(t, "pending", poll.Status)

	// 授权后：authorized + 账号信息
	pending = false
	poll, err = server.Poll(context.Background(), start.SessionID)
	require.NoError(t, err)
	require.Equal(t, "authorized", poll.Status)
	require.Equal(t, "nick-1", poll.Nickname)

	credentials, err := server.Credentials(start.SessionID)
	require.NoError(t, err)
	require.Equal(t, "at-1", credentials.AccessToken)
	require.Equal(t, "rt-1", credentials.RefreshToken)
	require.Equal(t, "u-1", credentials.UID)
	require.Equal(t, "e-1", credentials.EnterpriseID)
	require.Equal(t, "nick-1", credentials.Nickname)
	require.Greater(t, credentials.ExpiresAt, int64(0))
}

// TestCodebuddyLoginStartGlobal 区域参数归一化：global 走 workbuddy.ai 域。
func TestCodebuddyLoginStartGlobal(t *testing.T) {
	var seenHost string
	server := newCodebuddyLoginTestService(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.URL.Host
		_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{"state": "st-g", "authUrl": "https://example.com/g"}))
	}))
	start, err := server.Start(context.Background(), "global")
	require.NoError(t, err)
	require.Equal(t, "global", start.Region)
	require.Equal(t, "www.workbuddy.ai", seenHost)

	start, err = server.Start(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, "cn", start.Region)
}

// TestCodebuddyLoginSessionLifecycle 会话过期与未知会话错误。
func TestCodebuddyLoginSessionLifecycle(t *testing.T) {
	server := NewCodebuddyLoginService()
	_, err := server.Credentials("no-such-session")
	require.Error(t, err)

	poll, err := server.Poll(context.Background(), "no-such-session")
	require.Error(t, err)
	require.Nil(t, poll)
}

// TestCodebuddyLoginCredentialsNotAuthorized 未授权会话取凭证报错。
func TestCodebuddyLoginCredentialsNotAuthorized(t *testing.T) {
	server := newCodebuddyLoginTestService(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v2/plugin/auth/state" {
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{"state": "st-2", "authUrl": "https://example.com/auth"}))
			return
		}
		// token 轮询恒 pending（登录未完成）。
		_, _ = w.Write(codebuddyEnvelopeJSON(1004, "login ing", nil))
	}))
	start, err := server.Start(context.Background(), "cn")
	require.NoError(t, err)
	_, err = server.Credentials(start.SessionID)
	require.Error(t, err)
}
