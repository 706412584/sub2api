//go:build unit

package xai

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type ssoDeviceFakeClient struct {
	t             *testing.T
	tokenCalls    int
	cookieHeaders []string
	approveForm   url.Values
	approveOrigin string
	approveRefer  string
	// omitConsentToken 模拟 consent 页未返回 consent_token 的异常场景。
	omitConsentToken bool
}

func (c *ssoDeviceFakeClient) Do(req *http.Request) (*http.Response, error) {
	c.cookieHeaders = append(c.cookieHeaders, req.Header.Get("Cookie"))
	// consent 页会先被 verify 的 303 跟随到 /consent（无查询串），随后再显式请求
	// 带 user_code 的地址，两种形式都要接受。
	if strings.HasPrefix(req.URL.Path, "/oauth2/device/consent") {
		require.Equal(c.t, http.MethodGet, req.Method)
		if c.omitConsentToken {
			return ssoDeviceResponse(http.StatusOK, nil, `<html><form action="https://auth.x.ai/oauth2/device/approve" method="POST">`+
				`<input type="hidden" name="user_code" value="USER-1"/>`+
				`</form></html>`), nil
		}
		return ssoDeviceResponse(http.StatusOK, nil, `<html><form action="https://auth.x.ai/oauth2/device/approve" method="POST">`+
			`<input type="hidden" name="user_code" value="USER-1"/>`+
			`<input type="hidden" name="consent_token" value="consent-jwt-token"/>`+
			`</form></html>`), nil
	}
	switch req.URL.String() {
	case SSOAccountsURL:
		require.Equal(c.t, http.MethodGet, req.Method)
		return ssoDeviceResponse(http.StatusOK, http.Header{"Set-Cookie": {"session=web-session; Domain=x.ai; Path=/"}}, `{}`), nil
	case SSODeviceURL:
		require.Equal(c.t, http.MethodPost, req.Method)
		values := readSSODeviceForm(c.t, req)
		require.Equal(c.t, DefaultClientID, values.Get("client_id"))
		require.Equal(c.t, SSOBuildScope, values.Get("scope"))
		// Build 客户端标识：缺失时上游可能拒绝发放 Build scope。
		require.Equal(c.t, ssoDeviceReferrer, values.Get("referrer"))
		require.Equal(c.t, ssoDeviceUserAgent, req.Header.Get("User-Agent"))
		require.Equal(c.t, ssoDeviceVersion, req.Header.Get("x-grok-client-version"))
		require.Equal(c.t, ssoDeviceSurface, req.Header.Get("x-grok-client-surface"))
		return ssoDeviceResponse(http.StatusOK, http.Header{"Set-Cookie": {"csrf=csrf-token; Path=/"}}, `{"device_code":"device-1","user_code":"USER-1","verification_uri_complete":"https://auth.x.ai/oauth2/device/complete","interval":1,"expires_in":60}`), nil
	case "https://auth.x.ai/oauth2/device/complete":
		require.Equal(c.t, http.MethodGet, req.Method)
		return ssoDeviceResponse(http.StatusOK, nil, `<html>ok</html>`), nil
	case SSOVerifyURL:
		require.Equal(c.t, http.MethodPost, req.Method)
		values := readSSODeviceForm(c.t, req)
		require.Equal(c.t, "USER-1", values.Get("user_code"))
		return ssoDeviceResponse(http.StatusFound, http.Header{"Location": {"/oauth2/device/consent"}}, ``), nil
	case SSOApproveURL:
		require.Equal(c.t, http.MethodPost, req.Method)
		values := readSSODeviceForm(c.t, req)
		require.Equal(c.t, "USER-1", values.Get("user_code"))
		require.Equal(c.t, "allow", values.Get("action"))
		require.Equal(c.t, "User", values.Get("principal_type"))
		c.approveForm = values
		c.approveOrigin = req.Header.Get("Origin")
		c.approveRefer = req.Header.Get("Referer")
		return ssoDeviceResponse(http.StatusSeeOther, http.Header{"Location": {"/oauth2/device/done"}}, ``), nil
	case "https://auth.x.ai/oauth2/device/done":
		require.Equal(c.t, http.MethodGet, req.Method)
		return ssoDeviceResponse(http.StatusOK, nil, `<html>done</html>`), nil
	case SSOTokenURL:
		require.Equal(c.t, http.MethodPost, req.Method)
		c.tokenCalls++
		values := readSSODeviceForm(c.t, req)
		require.Equal(c.t, "urn:ietf:params:oauth:grant-type:device_code", values.Get("grant_type"))
		require.Equal(c.t, "device-1", values.Get("device_code"))
		return ssoDeviceResponse(http.StatusOK, nil, `{"access_token":"access-token","refresh_token":"refresh-token","id_token":"id-token","token_type":"Bearer","expires_in":3600,"scope":"`+SSOBuildScope+`"}`), nil
	default:
		c.t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
		return nil, nil
	}
}

func TestConvertSSOToBuildCompletesDeviceFlow(t *testing.T) {
	t.Setenv(EnvClientID, "")
	client := &ssoDeviceFakeClient{t: t}
	token, err := ConvertSSOToBuild(context.Background(), "sso=sso-token; ignored=1", &SSODeviceOptions{
		HTTPClient: client,
		Sleep: func(context.Context, time.Duration) error {
			return nil
		},
	})

	require.NoError(t, err)
	require.Equal(t, "access-token", token.AccessToken)
	require.Equal(t, "refresh-token", token.RefreshToken)
	require.Equal(t, "id-token", token.IDToken)
	require.Equal(t, SSOBuildScope, token.Scope)
	require.Equal(t, 1, client.tokenCalls)
	require.Contains(t, client.cookieHeaders[0], "sso=sso-token")
	require.Contains(t, client.cookieHeaders[0], "sso-rw=sso-token")
	require.Contains(t, client.cookieHeaders[len(client.cookieHeaders)-1], "session=web-session")
	require.Contains(t, client.cookieHeaders[len(client.cookieHeaders)-1], "csrf=csrf-token")

	// approve 必须回传 consent 页的 consent_token，否则上游 403。
	require.Equal(t, "consent-jwt-token", client.approveForm.Get("consent_token"))
	// approve 必须带 Origin/Referer 通过 CSRF 校验，否则上游 403
	// "Request could not be verified"。
	require.Equal(t, "https://accounts.x.ai", client.approveOrigin)
	require.Equal(t, "https://accounts.x.ai/", client.approveRefer)
}

// consent 页缺少 consent_token 时必须明确失败，而不是发出一个必然 403 的 approve。
func TestConvertSSOToBuildRequiresConsentToken(t *testing.T) {
	t.Setenv(EnvClientID, "")
	client := &ssoDeviceFakeClient{t: t, omitConsentToken: true}
	_, err := ConvertSSOToBuild(context.Background(), "sso=sso-token", &SSODeviceOptions{
		HTTPClient: client,
		Sleep:      func(context.Context, time.Duration) error { return nil },
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "consent token")
}

func TestExtractConsentToken(t *testing.T) {
	html := `<form action="https://auth.x.ai/oauth2/device/approve" method="POST">` +
		`<input type="hidden" name="user_code" value="ABC-123"/>` +
		`<input type="hidden" name="consent_token" value="tok-1"/>` +
		`</form>`
	require.Equal(t, "tok-1", extractConsentToken(html))
	require.Equal(t, "", extractConsentToken(`<html>no token here</html>`))
	require.Equal(t, "", extractConsentToken(`name="consent_token"`))
}

func TestNormalizeSSOTokenAcceptsCookieHeader(t *testing.T) {
	require.Equal(t, "token-1", NormalizeSSOToken("Cookie: foo=bar; sso=token-1; sso-rw=token-2"))
	require.Equal(t, "token-2", NormalizeSSOToken("sso-rw=token-2; foo=bar"))
	require.Equal(t, "raw-token", NormalizeSSOToken(" raw-token ; ignored=1"))
	require.Empty(t, NormalizeSSOToken(strings.Repeat("x", ssoMaxTokenLength+1)))
}

func TestSSODeviceCookieJarHonorsDomainAndPath(t *testing.T) {
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	flow := &ssoDeviceFlow{cookieJar: jar}
	accountsURL, err := url.Parse("https://accounts.x.ai/")
	require.NoError(t, err)
	authURL, err := url.Parse("https://auth.x.ai/oauth2/device/verify")
	require.NoError(t, err)

	flow.captureCookies(accountsURL, ssoDeviceResponse(http.StatusOK, http.Header{"Set-Cookie": {
		"host-only=accounts; Path=/",
		"shared=all-xai; Domain=x.ai; Path=/",
		"narrow=oauth-only; Domain=x.ai; Path=/oauth2",
	}}, ""))

	authCookies := flow.cookieHeader(authURL)
	require.NotContains(t, authCookies, "host-only=accounts")
	require.Contains(t, authCookies, "shared=all-xai")
	require.Contains(t, authCookies, "narrow=oauth-only")

	accountsCookies := flow.cookieHeader(accountsURL)
	require.Contains(t, accountsCookies, "host-only=accounts")
	require.Contains(t, accountsCookies, "shared=all-xai")
	require.NotContains(t, accountsCookies, "narrow=oauth-only")
}

func ssoDeviceResponse(status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func readSSODeviceForm(t *testing.T, req *http.Request) url.Values {
	t.Helper()
	data, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	values, err := url.ParseQuery(string(data))
	require.NoError(t, err)
	return values
}
