package codebuddy

import (
	"bytes"
	"encoding/json"
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

	// 账号激活（仅 Global）。登录只拿到 token，账号仍是「未激活」，
	// chat 会回 429 code=14017（"The trial version is not yet activated"）。
	// 网页登录会多做三步，网关必须自己补，顺序不可颠倒。
	PathUserAreaInfo  = "/billing/area/get-user-area-info"
	PathLoginAccount2 = "/console/login/account"
	PathRegisterCloud = "/auth/realms/copilot/overseas/user/register"
	PathTrial         = "/billing/ide/trial"
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

// ── 账号激活（仅 Global）────────────────────────────────────────────────────
//
// 登录只拿到 token，账号仍是「未激活」，chat 会回 429 code=14017
// （"The trial version is not yet activated"）。网页登录会多做三步：
//
//	1. POST /console/login/account        提交注册地（areaInfoComplete 翻 true）
//	2. GET  /auth/realms/copilot/overseas/user/register?userId=   registerCloud
//	3. POST /billing/ide/trial            试用激活（翻转 14017 的那一步）
//
// 顺序不可颠倒：跳过 1 直接调 2 会回 {"code":500,"msg":"register failed:register region required"}。
//
// 只对 Global 做：CN 的同名路径语义不同 —— get-user-area-info 返回 WAF 拦截 HTML
// 而非 JSON，billing/ide/trial 对已激活账号回 code=14051 "has applied trial"。
//
// 这些端点要求 Bearer + X-User-Id + X-No-Enterprise-Id + X-Domain + X-Product，
// 与 chat/billing 的头略有差异（未激活账号没有 enterpriseId，显式用 X-No- 声明）。

// ActivateHeaders 账号激活端点请求头。
func ActivateHeaders(req *http.Request, creds Credentials, r Region) {
	CommonHeaders(req, r)
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	if creds.UID != "" {
		req.Header.Set("X-User-Id", creds.UID)
	}
	if creds.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", creds.EnterpriseID)
	} else {
		req.Header.Set("X-No-Enterprise-Id", "1")
	}
	if creds.Domain != "" {
		req.Header.Set("X-Domain", creds.Domain)
	}
	req.Header.Set("X-Product", "SaaS")
}

// BuildUserAreaInfoRequest 构造查询注册地的请求（POST {"action":"getUserAreaInfo"}）。
func BuildUserAreaInfoRequest(creds Credentials, region Region, opts EndpointOptions) (*http.Request, error) {
	req, err := newRequest(http.MethodPost, opts.billingBase(region)+PathUserAreaInfo,
		[]byte(`{"action":"getUserAreaInfo"}`))
	if err != nil {
		return nil, err
	}
	ActivateHeaders(req, creds, region)
	return req, nil
}

// BuildSubmitAreaRequest 构造提交注册地的请求（POST，attributes 的值都是数组，与网页一致）。
func BuildSubmitAreaRequest(creds Credentials, region Region, countryCode, countryFullName, countryName string, opts EndpointOptions) (*http.Request, error) {
	body, err := json.Marshal(map[string]any{
		"attributes": map[string]any{
			"countryCode":     []string{countryCode},
			"countryFullName": []string{countryFullName},
			"countryName":     []string{countryName},
		},
	})
	if err != nil {
		return nil, err
	}
	req, err := newRequest(http.MethodPost, opts.billingBase(region)+PathLoginAccount2, body)
	if err != nil {
		return nil, err
	}
	ActivateHeaders(req, creds, region)
	return req, nil
}

// BuildRegisterCloudRequest 构造 registerCloud 请求（GET ?userId=）。
func BuildRegisterCloudRequest(creds Credentials, region Region, uid string, opts EndpointOptions) (*http.Request, error) {
	u := opts.billingBase(region) + PathRegisterCloud + "?userId=" + url.QueryEscape(uid)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	ActivateHeaders(req, creds, region)
	return req, nil
}

// BuildTrialRequest 构造试用激活请求（POST {}）。
func BuildTrialRequest(creds Credentials, region Region, opts EndpointOptions) (*http.Request, error) {
	req, err := newRequest(http.MethodPost, opts.billingBase(region)+PathTrial, []byte("{}"))
	if err != nil {
		return nil, err
	}
	ActivateHeaders(req, creds, region)
	return req, nil
}

// CodeTrialAlreadyApplied 试用已领过（对已激活账号调 trial 的正常返回，不算失败）。
const CodeTrialAlreadyApplied = 14051

// CodeTrialNotActivated 账号未激活（chat 直接返回此码，需先跑激活三步）。
const CodeTrialNotActivated = 14017
