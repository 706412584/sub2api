package codebuddy

import "net/http"

// 客户端身份与来源（与 CodeBuddy 官方 CLI 对齐）。
const (
	ClientUA          = "CLI/2.63.2 CodeBuddy/2.63.2"
	OriginRefererCN   = "https://www.codebuddy.cn"
	OriginRefererGlob = "https://www.workbuddy.ai"
)

// OriginRefererFor 返回区域的 Origin/Referer 根。
func OriginRefererFor(r Region) string {
	if r == RegionGlobal {
		return OriginRefererGlob
	}
	return OriginRefererCN
}

// CommonHeaders 设置所有 API 共享的请求头。
func CommonHeaders(req *http.Request, r Region) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	origin := OriginRefererFor(r)
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("User-Agent", ClientUA)
}

// ChatHeaders 在 common 之上加 chat 专属的账号头。
// 缺省字段用 X-No-* 约定（与 CodeBuddy 官方 CLI 一致）。
//
// 安全红线：绝不在 chat 请求里携带 X-Refresh-Token。
func ChatHeaders(req *http.Request, creds Credentials, r Region) {
	CommonHeaders(req, r)
	if creds.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	} else {
		req.Header.Set("X-No-Authorization", "1")
	}
	if creds.UID != "" {
		req.Header.Set("X-User-Id", creds.UID)
	} else {
		req.Header.Set("X-No-User-Id", "1")
	}
	if creds.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", creds.EnterpriseID)
	} else {
		req.Header.Set("X-No-Enterprise-Id", "1")
	}
	if creds.Domain != "" {
		req.Header.Set("X-Domain", creds.Domain)
	} else {
		req.Header.Set("X-No-Department-Info", "1")
	}
	req.Header.Set("X-Product", "SaaS")
}

// BillingHeaders billing 接口请求头。
func BillingHeaders(req *http.Request, creds Credentials, r Region) {
	CommonHeaders(req, r)
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	if creds.UID != "" {
		req.Header.Set("X-User-Id", creds.UID)
	}
	if creds.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", creds.EnterpriseID)
		req.Header.Set("X-Tenant-Id", creds.EnterpriseID)
	}
	if creds.Domain != "" {
		req.Header.Set("X-Domain", creds.Domain)
	}
}

// RefreshHeaders refresh 端点专属头（X-Refresh-Token 只允许出现在这里）。
func RefreshHeaders(req *http.Request, creds Credentials, r Region) {
	CommonHeaders(req, r)
	req.Header.Set("X-Refresh-Token", creds.RefreshToken)
	if creds.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", creds.EnterpriseID)
	}
	req.Header.Set("X-Auth-Refresh-Source", "workbuddy")
}

// AuthFlowHeaders 插件鉴权（state/token/login-account）请求头。
// 无凭证阶段只能带 common 头 + 空 JSON body。
func AuthFlowHeaders(req *http.Request, r Region) {
	CommonHeaders(req, r)
}
