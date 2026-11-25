package codebuddy

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func testCreds() Credentials {
	return Credentials{
		AccessToken:  "at-1",
		RefreshToken: "rt-1",
		UID:          "u-1",
		EnterpriseID: "e-1",
		Domain:       "example.com",
	}
}

func TestBasesByRegion(t *testing.T) {
	if ChatBase(RegionCN) != ChatBaseCN {
		t.Errorf("CN chat base=%q", ChatBase(RegionCN))
	}
	if ChatBase(RegionGlobal) != ChatBaseGlobal {
		t.Errorf("global chat base=%q", ChatBase(RegionGlobal))
	}
	if BillingBase(RegionCN) != BillingBaseCN {
		t.Errorf("CN billing base=%q", BillingBase(RegionCN))
	}
	if BillingBase(RegionGlobal) != BillingBaseGl {
		t.Errorf("global billing base=%q", BillingBase(RegionGlobal))
	}
}

func TestEndpointOptionsOverride(t *testing.T) {
	opts := EndpointOptions{ChatBase: " https://chat.example/ ", BillingBase: "https://bill.example/"}
	if got := opts.chatBase(RegionCN); got != "https://chat.example" {
		t.Errorf("chatBase=%q want trimmed override", got)
	}
	if got := opts.billingBase(RegionGlobal); got != "https://bill.example" {
		t.Errorf("billingBase=%q want trimmed override", got)
	}
	// 空 override 回落区域默认
	empty := EndpointOptions{}
	if got := empty.chatBase(RegionGlobal); got != ChatBaseGlobal {
		t.Errorf("empty override chatBase=%q want %q", got, ChatBaseGlobal)
	}
	if got := empty.billingBase(RegionCN); got != BillingBaseCN {
		t.Errorf("empty override billingBase=%q want %q", got, BillingBaseCN)
	}
}

func TestBuildChatRequest(t *testing.T) {
	for _, tc := range []struct {
		region     Region
		wantURL    string
		wantOrigin string
	}{
		{RegionCN, ChatBaseCN + PathChatCompletions, OriginRefererCN},
		{RegionGlobal, ChatBaseGlobal + PathChatCompletions, OriginRefererGlob},
	} {
		t.Run(string(tc.region), func(t *testing.T) {
			body := []byte(`{"model":"glm-5.2","stream":true}`)
			req, err := BuildChatRequest(testCreds(), tc.region, body, EndpointOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if req.Method != http.MethodPost {
				t.Errorf("method=%s want POST", req.Method)
			}
			if req.URL.String() != tc.wantURL {
				t.Errorf("url=%q want %q", req.URL.String(), tc.wantURL)
			}
			got, _ := io.ReadAll(req.Body)
			if string(got) != string(body) {
				t.Errorf("body=%q want %q", got, body)
			}
			if req.Header.Get("Origin") != tc.wantOrigin {
				t.Errorf("origin=%q want %q", req.Header.Get("Origin"), tc.wantOrigin)
			}
			if req.Header.Get("Authorization") != "Bearer at-1" {
				t.Errorf("authorization=%q", req.Header.Get("Authorization"))
			}
			if req.Header.Get("X-User-Id") != "u-1" {
				t.Errorf("X-User-Id=%q", req.Header.Get("X-User-Id"))
			}
			if req.Header.Get("X-Enterprise-Id") != "e-1" {
				t.Errorf("X-Enterprise-Id=%q", req.Header.Get("X-Enterprise-Id"))
			}
			if req.Header.Get("X-Product") != "SaaS" {
				t.Errorf("X-Product=%q", req.Header.Get("X-Product"))
			}
			// 安全红线：chat 请求绝不携带 X-Refresh-Token
			if v := req.Header.Get("X-Refresh-Token"); v != "" {
				t.Errorf("chat request must NOT carry X-Refresh-Token, got %q", v)
			}
		})
	}
}

func TestBuildModelsRequest(t *testing.T) {
	req, err := BuildModelsRequest(testCreds(), RegionCN, EndpointOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodGet {
		t.Errorf("method=%s want GET", req.Method)
	}
	if want := ChatBaseCN + PathModels; req.URL.String() != want {
		t.Errorf("url=%q want %q", req.URL.String(), want)
	}
	if req.Header.Get("X-Refresh-Token") != "" {
		t.Error("models request must NOT carry X-Refresh-Token")
	}
}

func TestBuildBillingRequests(t *testing.T) {
	t.Run("user resource", func(t *testing.T) {
		req, err := BuildUserResourceRequest(testCreds(), RegionCN, []byte(`{"PageNumber":1}`), EndpointOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if want := BillingBaseCN + PathUserResource; req.URL.String() != want {
			t.Errorf("url=%q want %q", req.URL.String(), want)
		}
		if req.Header.Get("X-Tenant-Id") != "e-1" {
			t.Errorf("X-Tenant-Id=%q want e-1", req.Header.Get("X-Tenant-Id"))
		}
	})

	t.Run("daily checkin 带空 JSON body", func(t *testing.T) {
		req, err := BuildDailyCheckinRequest(testCreds(), RegionGlobal, EndpointOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if want := BillingBaseGl + PathDailyCheckin; req.URL.String() != want {
			t.Errorf("url=%q want %q", req.URL.String(), want)
		}
		body, _ := io.ReadAll(req.Body)
		if string(body) != "{}" {
			t.Errorf("body=%q want {}", body)
		}
	})

	t.Run("report", func(t *testing.T) {
		payload := []byte(`[{"event":"chat_request_send","userId":"u-1"}]`)
		req, err := BuildReportRequest(testCreds(), RegionCN, payload, EndpointOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if want := BillingBaseCN + PathReport; req.URL.String() != want {
			t.Errorf("url=%q want %q", req.URL.String(), want)
		}
		body, _ := io.ReadAll(req.Body)
		if string(body) != string(payload) {
			t.Errorf("body=%q", body)
		}
	})
}

func TestBuildRefreshRequest(t *testing.T) {
	req, err := BuildRefreshRequest(testCreds(), RegionCN, EndpointOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if want := ChatBaseCN + PathTokenRefresh; req.URL.String() != want {
		t.Errorf("url=%q want %q", req.URL.String(), want)
	}
	// refresh 端点是唯一允许携带 X-Refresh-Token 的地方
	if req.Header.Get("X-Refresh-Token") != "rt-1" {
		t.Errorf("X-Refresh-Token=%q want rt-1", req.Header.Get("X-Refresh-Token"))
	}
	if req.Header.Get("X-Auth-Refresh-Source") != "workbuddy" {
		t.Errorf("X-Auth-Refresh-Source=%q", req.Header.Get("X-Auth-Refresh-Source"))
	}
	if req.Header.Get("X-Enterprise-Id") != "e-1" {
		t.Errorf("X-Enterprise-Id=%q", req.Header.Get("X-Enterprise-Id"))
	}
}

func TestBuildAuthFlowRequests(t *testing.T) {
	t.Run("state 带 platform=CLI 与空 JSON body", func(t *testing.T) {
		req, err := BuildAuthStateRequest(RegionGlobal, AuthFlowOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if req.Method != http.MethodPost {
			t.Errorf("method=%s want POST", req.Method)
		}
		if !strings.Contains(req.URL.Path, PathAuthState) {
			t.Errorf("path=%q", req.URL.Path)
		}
		if req.URL.Query().Get("platform") != "CLI" {
			t.Errorf("platform=%q want CLI", req.URL.Query().Get("platform"))
		}
		body, _ := io.ReadAll(req.Body)
		if string(body) != "{}" {
			t.Errorf("body=%q want {}", body)
		}
		// 无凭证阶段不得带 Authorization
		if v := req.Header.Get("Authorization"); v != "" {
			t.Errorf("auth flow must not carry Authorization, got %q", v)
		}
	})

	t.Run("token 轮询 state 转义", func(t *testing.T) {
		req, err := BuildAuthTokenRequest(RegionCN, "a b&c", AuthFlowOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if req.Method != http.MethodGet {
			t.Errorf("method=%s want GET", req.Method)
		}
		if got := req.URL.Query().Get("state"); got != "a b&c" {
			t.Errorf("state=%q want %q", got, "a b&c")
		}
	})

	t.Run("login account", func(t *testing.T) {
		req, err := BuildLoginAccountRequest(RegionCN, "s-1", AuthFlowOptions{Base: "https://auth.example/"})
		if err != nil {
			t.Fatal(err)
		}
		if want := "https://auth.example" + PathLoginAccount; !strings.HasPrefix(req.URL.String(), want) {
			t.Errorf("url=%q want prefix %q", req.URL.String(), want)
		}
		if got := req.URL.Query().Get("state"); got != "s-1" {
			t.Errorf("state=%q", got)
		}
	})
}

// TestHeadersFallbackToXNo 缺省字段用 X-No-* 约定（与 CodeBuddy 官方 CLI 一致）。
func TestHeadersFallbackToXNo(t *testing.T) {
	req, err := BuildChatRequest(Credentials{AccessToken: "t"}, RegionCN, []byte(`{}`), EndpointOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"X-No-User-Id", "X-No-Enterprise-Id", "X-No-Department-Info"} {
		if req.Header.Get(k) != "1" {
			t.Errorf("%s=%q want 1", k, req.Header.Get(k))
		}
	}
	if req.Header.Get("X-User-Id") != "" || req.Header.Get("X-Enterprise-Id") != "" {
		t.Error("empty credential fields must not be set as real headers")
	}

	// 无 access token 时用 X-No-Authorization
	req2, err := BuildChatRequest(Credentials{}, RegionCN, []byte(`{}`), EndpointOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if req2.Header.Get("X-No-Authorization") != "1" {
		t.Errorf("X-No-Authorization=%q want 1", req2.Header.Get("X-No-Authorization"))
	}
}

func TestCommonHeadersUA(t *testing.T) {
	req, err := BuildModelsRequest(testCreds(), RegionCN, EndpointOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("User-Agent") != ClientUA {
		t.Errorf("UA=%q want %q", req.Header.Get("User-Agent"), ClientUA)
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type=%q", req.Header.Get("Content-Type"))
	}
	if !strings.HasPrefix(req.Header.Get("Referer"), OriginRefererCN) {
		t.Errorf("Referer=%q", req.Header.Get("Referer"))
	}
}
