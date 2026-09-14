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

// TestCodebuddyLoginGlobalRunsActivation Global 登录成功后必须补跑激活三步。
// 不补的话账号是「未激活」态，chat 回 429 code=14017。
func TestCodebuddyLoginGlobalRunsActivation(t *testing.T) {
	var calls []string
	svc := newCodebuddyLoginTestService(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{"state": "st-g", "authUrl": "https://example.com/g"}))
		case "/v2/plugin/auth/token":
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{
				"accessToken": "at-g", "refreshToken": "rt-g", "expiresIn": 3600, "domain": "www.workbuddy.ai",
			}))
		case "/v2/plugin/login/account":
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{"uid": "u-g", "nickname": "nick-g"}))
		case "/billing/area/get-user-area-info":
			// data 是嵌套 JSON 字符串，解两层。
			inner, _ := json.Marshal(map[string]any{"IOS2": "US", "enName": "United States", "code": "1"})
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", string(inner)))
		case "/console/login/account":
			require.Contains(t, r.Header.Get("Authorization"), "Bearer at-g")
			require.Equal(t, "u-g", r.Header.Get("X-User-Id"))
			require.Equal(t, "1", r.Header.Get("X-No-Enterprise-Id"), "未激活账号无 enterpriseId，须显式声明")
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{}))
		case "/auth/realms/copilot/overseas/user/register":
			// 成功时返回 code=200 而非 0 —— 必须容忍，不得中断后续 trial。
			_, _ = w.Write(codebuddyEnvelopeJSON(200, "ok", nil))
		case "/billing/ide/trial":
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{}))
		default:
			http.NotFound(w, r)
		}
	}))

	start, err := svc.Start(context.Background(), "global")
	require.NoError(t, err)
	poll, err := svc.Poll(context.Background(), start.SessionID)
	require.NoError(t, err)
	require.Equal(t, "authorized", poll.Status)

	// 三步必须全部执行，且顺序不可颠倒（跳过注册地会导致 registerCloud 回 500）。
	require.Contains(t, calls, "POST /billing/area/get-user-area-info")
	require.Contains(t, calls, "POST /console/login/account")
	require.Contains(t, calls, "GET /auth/realms/copilot/overseas/user/register")
	require.Contains(t, calls, "POST /billing/ide/trial")

	idxArea := indexOfCall(calls, "POST /console/login/account")
	idxRegister := indexOfCall(calls, "GET /auth/realms/copilot/overseas/user/register")
	idxTrial := indexOfCall(calls, "POST /billing/ide/trial")
	require.Less(t, idxArea, idxRegister, "提交注册地必须在 registerCloud 之前")
	require.Less(t, idxRegister, idxTrial, "registerCloud 必须在 trial 之前")
}

// TestCodebuddyLoginCNSkipsActivation CN 区不得调用激活端点（同名路径语义不同）。
func TestCodebuddyLoginCNSkipsActivation(t *testing.T) {
	var calls []string
	svc := newCodebuddyLoginTestService(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		calls = append(calls, r.URL.Path)
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{"state": "st-c", "authUrl": "https://example.com/c"}))
		case "/v2/plugin/auth/token":
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{"accessToken": "at-c", "refreshToken": "rt-c", "expiresIn": 3600}))
		case "/v2/plugin/login/account":
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{"uid": "u-c"}))
		default:
			http.NotFound(w, r)
		}
	}))

	start, err := svc.Start(context.Background(), "cn")
	require.NoError(t, err)
	_, err = svc.Poll(context.Background(), start.SessionID)
	require.NoError(t, err)

	for _, c := range calls {
		require.NotContains(t, c, "trial", "CN 不应调用 trial")
		require.NotContains(t, c, "register", "CN 不应调用 registerCloud")
		require.NotContains(t, c, "get-user-area-info", "CN 不应调用注册地查询")
	}
}

// TestCodebuddyLoginActivationFailureDoesNotBlockLogin 激活失败不得阻断登录：
// token 已到手，凭证仍须产出（账号只是未激活，需要重新登录或人工处理）。
func TestCodebuddyLoginActivationFailureDoesNotBlockLogin(t *testing.T) {
	svc := newCodebuddyLoginTestService(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{"state": "st-f", "authUrl": "https://example.com/f"}))
		case "/v2/plugin/auth/token":
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{"accessToken": "at-f", "refreshToken": "rt-f", "expiresIn": 3600}))
		case "/v2/plugin/login/account":
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{"uid": "u-f"}))
		default:
			// 所有激活端点一律 500。
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":500,"msg":"boom"}`))
		}
	}))

	start, err := svc.Start(context.Background(), "global")
	require.NoError(t, err)
	poll, err := svc.Poll(context.Background(), start.SessionID)
	require.NoError(t, err)
	require.Equal(t, "authorized", poll.Status, "激活失败不应把登录判为失败")

	creds, err := svc.Credentials(start.SessionID)
	require.NoError(t, err)
	require.Equal(t, "at-f", creds.AccessToken)
	require.Equal(t, "u-f", creds.UID)
}

// TestCodebuddyLoginActivationTrialAlreadyApplied 14051「已领过试用」视作成功，不报失败。
func TestCodebuddyLoginActivationTrialAlreadyApplied(t *testing.T) {
	var trialSeen bool
	svc := newCodebuddyLoginTestService(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{"state": "st-t", "authUrl": "https://example.com/t"}))
		case "/v2/plugin/auth/token":
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{"accessToken": "at-t", "refreshToken": "rt-t", "expiresIn": 3600}))
		case "/v2/plugin/login/account":
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{"uid": "u-t"}))
		case "/billing/ide/trial":
			trialSeen = true
			_, _ = w.Write(codebuddyEnvelopeJSON(14051, "has applied trial", nil))
		default:
			_, _ = w.Write(codebuddyEnvelopeJSON(0, "", map[string]any{}))
		}
	}))

	start, err := svc.Start(context.Background(), "global")
	require.NoError(t, err)
	poll, err := svc.Poll(context.Background(), start.SessionID)
	require.NoError(t, err)
	require.Equal(t, "authorized", poll.Status)
	require.True(t, trialSeen, "trial 端点应被调用")
}

func indexOfCall(calls []string, want string) int {
	for i, c := range calls {
		if c == want {
			return i
		}
	}
	return -1
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
