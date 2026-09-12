package codebuddy

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
)

// 区域端点（实测，2026-09）。
//
// CN：chat 走 copilot.tencent.com（与模型接口 data.endpoint 返回一致），
// billing/report 走 www.codebuddy.cn。
// Global：chat 与 billing 共用 www.workbuddy.ai。
const (
	ChatBaseCN     = "https://copilot.tencent.com"
	BillingBaseCN  = "https://www.codebuddy.cn"
	ChatBaseGlobal = "https://www.workbuddy.ai"
	BillingBaseGl  = "https://www.workbuddy.ai"
)

// 上游路径。
const (
	PathChatCompletions = "/v2/chat/completions"
	PathModels          = "/console/enterprises/personal/models"
	PathUserResource    = "/v2/billing/meter/get-user-resource"
	PathDailyCheckin    = "/v2/billing/meter/daily-checkin"
	PathReport          = "/v2/report"
	PathTokenRefresh    = "/v2/plugin/auth/token/refresh"
	PathAuthState       = "/v2/plugin/auth/state"
	PathAuthToken       = "/v2/plugin/auth/token"
	PathLoginAccount    = "/v2/plugin/login/account"
)

// ChatBase 返回区域的 chat 端点根。
func ChatBase(r Region) string {
	if r == RegionGlobal {
		return ChatBaseGlobal
	}
	return ChatBaseCN
}

// BillingBase 返回区域的 billing 端点根（report / 签到 / 余额均在此）。
func BillingBase(r Region) string {
	if r == RegionGlobal {
		return BillingBaseGl
	}
	return BillingBaseCN
}

// EndpointOptions 覆盖端点根，便于测试与自建反代。
type EndpointOptions struct {
	ChatBase    string
	BillingBase string
}

func (o EndpointOptions) chatBase(r Region) string {
	if strings.TrimSpace(o.ChatBase) != "" {
		return strings.TrimRight(strings.TrimSpace(o.ChatBase), "/")
	}
	return ChatBase(r)
}

func (o EndpointOptions) billingBase(r Region) string {
	if strings.TrimSpace(o.BillingBase) != "" {
		return strings.TrimRight(strings.TrimSpace(o.BillingBase), "/")
	}
	return BillingBase(r)
}

// newRequest 构造 GET/POST 请求；body 为 nil 时不带 body。
func newRequest(method, rawURL string, body []byte) (*http.Request, error) {
	if body == nil {
		return http.NewRequest(method, rawURL, nil)
	}
	return http.NewRequest(method, rawURL, bytes.NewReader(body))
}

// BuildChatRequest 构造 chat completions 请求（POST，body 已由 PrepareBody 改写）。
func BuildChatRequest(creds Credentials, region Region, body []byte, opts EndpointOptions) (*http.Request, error) {
	req, err := newRequest(http.MethodPost, opts.chatBase(region)+PathChatCompletions, body)
	if err != nil {
		return nil, err
	}
	ChatHeaders(req, creds, region)
	return req, nil
}

// BuildModelsRequest 构造动态模型列表请求（GET）。
func BuildModelsRequest(creds Credentials, region Region, opts EndpointOptions) (*http.Request, error) {
	req, err := newRequest(http.MethodGet, opts.chatBase(region)+PathModels, nil)
	if err != nil {
		return nil, err
	}
	ChatHeaders(req, creds, region)
	return req, nil
}

// BuildUserResourceRequest 构造余额查询请求（POST，body 由调用方给定 JSON）。
func BuildUserResourceRequest(creds Credentials, region Region, body []byte, opts EndpointOptions) (*http.Request, error) {
	req, err := newRequest(http.MethodPost, opts.billingBase(region)+PathUserResource, body)
	if err != nil {
		return nil, err
	}
	BillingHeaders(req, creds, region)
	return req, nil
}

// BuildDailyCheckinRequest 构造每日签到请求（POST {}）。
func BuildDailyCheckinRequest(creds Credentials, region Region, opts EndpointOptions) (*http.Request, error) {
	req, err := newRequest(http.MethodPost, opts.billingBase(region)+PathDailyCheckin, []byte("{}"))
	if err != nil {
		return nil, err
	}
	BillingHeaders(req, creds, region)
	return req, nil
}

// BuildReportRequest 构造活跃上报请求（POST，body 为事件数组 JSON）。
func BuildReportRequest(creds Credentials, region Region, body []byte, opts EndpointOptions) (*http.Request, error) {
	req, err := newRequest(http.MethodPost, opts.billingBase(region)+PathReport, body)
	if err != nil {
		return nil, err
	}
	BillingHeaders(req, creds, region)
	return req, nil
}

// BuildRefreshRequest 构造 token 刷新请求（POST，无 body）。
// 这是唯一允许携带 X-Refresh-Token 的端点。
func BuildRefreshRequest(creds Credentials, region Region, opts EndpointOptions) (*http.Request, error) {
	req, err := newRequest(http.MethodPost, opts.chatBase(region)+PathTokenRefresh, nil)
	if err != nil {
		return nil, err
	}
	RefreshHeaders(req, creds, region)
	return req, nil
}

// AuthFlowOptions 覆盖插件鉴权端点的 host（默认与 chat 端点同源）。
type AuthFlowOptions struct {
	Base string
}

func (o AuthFlowOptions) base(r Region) string {
	if strings.TrimSpace(o.Base) != "" {
		return strings.TrimRight(strings.TrimSpace(o.Base), "/")
	}
	return ChatBase(r)
}

// BuildAuthStateRequest 构造 OAuth state 申请请求（POST，platform=CLI）。
func BuildAuthStateRequest(region Region, opts AuthFlowOptions) (*http.Request, error) {
	u := opts.base(region) + PathAuthState + "?platform=" + url.QueryEscape("CLI")
	req, err := newRequest(http.MethodPost, u, []byte("{}"))
	if err != nil {
		return nil, err
	}
	AuthFlowHeaders(req, region)
	return req, nil
}

// BuildAuthTokenRequest 构造按 state 轮询 token 的请求（GET）。
func BuildAuthTokenRequest(region Region, state string, opts AuthFlowOptions) (*http.Request, error) {
	u := opts.base(region) + PathAuthToken + "?state=" + url.QueryEscape(state)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	AuthFlowHeaders(req, region)
	return req, nil
}

// BuildLoginAccountRequest 构造按 state 查询账号信息的请求（GET）。
func BuildLoginAccountRequest(region Region, state string, opts AuthFlowOptions) (*http.Request, error) {
	u := opts.base(region) + PathLoginAccount + "?state=" + url.QueryEscape(state)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	AuthFlowHeaders(req, region)
	return req, nil
}
