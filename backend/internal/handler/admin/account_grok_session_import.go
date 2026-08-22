package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Grok Console / Web 会话批量导入。
// 直接接收外部工具导出的 JSON：
//
//	{
//	  "provider": "grok_web" | "grok_console",
//	  "accounts": [
//	    {
//	      "name": "...", "email": "...",
//	      "sso_token": "...",
//	      "sso_rw_token": "...",            // 可选
//	      "cloudflare_cookies": "cf_clearance=...", // Web 必填；兼容 cf_clearance 键
//	      "user_agent": "Mozilla/5.0 ...",
//	      "proxy": "http://127.0.0.1:7887"  // 或 proxy_id
//	    }
//	  ]
//	}
//
// proxy 支持三种写法：已有代理的数字 ID、代理名称精确匹配、代理 URL 匹配
// （按 ListActive 的 protocol://host:port 归一化比对）。找不到匹配时报错，
// 不静默落到直连——会话材料与出口绑定是安全边界。

type grokSessionImportRequest struct {
	Data         any      `json:"data"`
	Name         string   `json:"name"`
	Notes        string   `json:"notes"`
	GroupIDs     []int64  `json:"group_ids"`
	ProxyID      *int64   `json:"proxy_id"`
	Provider     string   `json:"provider"`
	Accounts     []any    `json:"accounts"`
	Concurrency  *int     `json:"concurrency"`
	Priority     *int     `json:"priority"`
	RateMultiplier *float64 `json:"rate_multiplier"`
}

type grokSessionImportEntry struct {
	Name             string
	Email            string
	SSOToken         string
	SSORWToken       string
	CFClearance      string
	BrowserUserAgent string
	ProxyRef         string // 数字 ID / 代理名 / URL 字符串
	ProxyID          *int64
	Source           string // console | web
}

type grokSessionImportItemResult struct {
	Index     int    `json:"index"`
	Name      string `json:"name,omitempty"`
	AccountID int64  `json:"account_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

type grokSessionImportResponse struct {
	Created []grokSessionImportItemResult `json:"created"`
	Failed  []grokSessionImportItemResult `json:"failed"`
}

// ImportGrokSessions 批量导入 Grok Console / Web 会话账号
func (h *AccountHandler) ImportGrokSessions(c *gin.Context) {
	if h.grokSession == nil {
		response.ErrorFrom(c, service.ErrGrokSessionUnavailable)
		return
	}
	var req grokSessionImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	entries, err := parseGrokSessionImportData(req)
	if err != nil {
		response.BadRequest(c, "Failed to parse Grok session data: "+err.Error())
		return
	}
	if len(entries) == 0 {
		response.BadRequest(c, "No valid Grok session accounts found in the input data")
		return
	}

	executeAdminIdempotentJSON(c, "admin.accounts.import_grok_session", req, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		return h.importGrokSessionEntries(ctx, req, entries)
	})
}

// parseGrokSessionImportData 兼容四种输入形态：
//   - 完整导出格式 {provider, accounts:[...]}
//   - 顶层数组 [...]
//   - {accounts:[...]}（无 provider，按 data.provider 或逐条推断）
//   - 单账号对象 {...}
func parseGrokSessionImportData(req grokSessionImportRequest) ([]grokSessionImportEntry, error) {
	rawList := req.Accounts
	defaultSource := normalizeGrokSessionSource(req.Provider)
	if len(rawList) == 0 && req.Data != nil {
		switch v := req.Data.(type) {
		case []any:
			rawList = v
		case map[string]any:
			if accounts, ok := v["accounts"].([]any); ok {
				rawList = accounts
			}
			if defaultSource == "" {
				if p, ok := v["provider"].(string); ok {
					defaultSource = normalizeGrokSessionSource(p)
				}
			}
		}
	}

	entries := make([]grokSessionImportEntry, 0, len(rawList))
	for _, raw := range rawList {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		entry := grokSessionImportEntry{
			Name:             jsonString(obj, "name"),
			Email:            jsonString(obj, "email"),
			SSOToken:         firstNonEmptyGrok(jsonString(obj, "sso_token"), jsonString(obj, "token"), stripCookieValue(obj)),
			SSORWToken:       jsonString(obj, "sso_rw_token"),
			CFClearance:      extractCFClearance(obj),
			BrowserUserAgent: firstNonEmptyGrok(jsonString(obj, "user_agent"), jsonString(obj, "browser_user_agent")),
			ProxyRef:         jsonString(obj, "proxy"),
		}
		if id := jsonInt64(obj, "proxy_id"); id > 0 {
			idCopy := id
			entry.ProxyID = &idCopy
		}
		source := defaultSource
		if s := normalizeGrokSessionSource(jsonString(obj, "provider")); s != "" {
			source = s
		}
		if source == "" {
			// 按字段推断：带 cf_clearance 视为 Web，否则 Console
			if entry.CFClearance != "" {
				source = "web"
			} else {
				source = "console"
			}
		}
		entry.Source = source

		// SSO token 可能是整段 cookie 头或 "sso=...; sso-rw=..." 形式
		entry.SSOToken = xai.NormalizeSSOToken(entry.SSOToken)
		entry.SSORWToken = xai.NormalizeSSOToken(entry.SSORWToken)
		if entry.Name == "" && entry.Email != "" {
			entry.Name = entry.Email
		}
		if entry.SSOToken != "" && entry.BrowserUserAgent != "" {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func (h *AccountHandler) importGrokSessionEntries(
	ctx context.Context,
	req grokSessionImportRequest,
	entries []grokSessionImportEntry,
) (any, error) {
	result := &grokSessionImportResponse{
		Created: make([]grokSessionImportItemResult, 0, len(entries)),
		Failed:  make([]grokSessionImportItemResult, 0),
	}

	for i, entry := range entries {
		item := grokSessionImportItemResult{Index: i + 1, Name: entry.Name}
		if err := h.importOneGrokSession(ctx, req, entry); err != nil {
			item.Error = err.Error()
			result.Failed = append(result.Failed, item)
			continue
		}
		result.Created = append(result.Created, item)
	}
	return result, nil
}

func (h *AccountHandler) importOneGrokSession(
	ctx context.Context,
	req grokSessionImportRequest,
	entry grokSessionImportEntry,
) error {
	proxyID := entry.ProxyID
	if proxyID == nil && req.ProxyID != nil {
		proxyID = req.ProxyID
	}
	if proxyID == nil && strings.TrimSpace(entry.ProxyRef) != "" {
		resolved, err := h.resolveGrokSessionProxy(ctx, entry.ProxyRef)
		if err != nil {
			return err
		}
		proxyID = resolved
	}
	if proxyID == nil {
		return fmt.Errorf("proxy is required (set \"proxy\" to an existing proxy id, name, or URL)")
	}

	name := strings.TrimSpace(entry.Name)
	if name == "" {
		name = fmt.Sprintf("Grok %s %s", strings.Title(entry.Source), xai.NormalizeSSOToken(entry.SSOToken)[:min(8, len(xai.NormalizeSSOToken(entry.SSOToken)))])
	}
	accountName := name

	var notes *string
	if trimmed := strings.TrimSpace(req.Notes); trimmed != "" {
		notes = &trimmed
	}
	input := &service.CreateAccountInput{
		Name:           accountName,
		Notes:          notes,
		Platform:       service.PlatformGrok,
		Type:           grokSessionAccountType(entry.Source),
		Credentials:    map[string]any{"placeholder": true},
		ProxyID:        proxyID,
		GroupIDs:       req.GroupIDs,
		RateMultiplier: req.RateMultiplier,
	}
	if req.Concurrency != nil {
		input.Concurrency = *req.Concurrency
	}
	if req.Priority != nil {
		input.Priority = *req.Priority
	}

	account, err := h.adminService.CreateAccount(ctx, input)
	if err != nil {
		return fmt.Errorf("create account: %w", err)
	}

	saveErr := func() error {
		if entry.Source == "web" {
			return h.grokSession.SaveWebSession(
				ctx, account.ID,
				entry.SSOToken, entry.SSORWToken, entry.CFClearance,
				entry.BrowserUserAgent, proxyID,
			)
		}
		return h.grokSession.SaveConsoleSession(
			ctx, account.ID,
			entry.SSOToken, entry.SSORWToken,
			entry.BrowserUserAgent, proxyID,
		)
	}()
	if saveErr != nil {
		// 会话导入失败：回滚空壳账号，避免残留不可用条目
		_ = h.adminService.DeleteAccount(ctx, account.ID)
		return fmt.Errorf("import session: %w", saveErr)
	}
	return nil
}

// resolveGrokSessionProxy 把 "7887" / "my-proxy" / "http://127.0.0.1:7887"
// 解析为已有代理 ID。名称与 URL 均要求精确匹配，不做模糊猜测。
func (h *AccountHandler) resolveGrokSessionProxy(ctx context.Context, ref string) (*int64, error) {
	ref = strings.TrimSpace(ref)
	proxies, err := h.adminService.GetAllProxies(ctx)
	if err != nil {
		return nil, fmt.Errorf("list proxies: %w", err)
	}
	// 纯数字 → 先按 ID 找
	if allDigits(ref) {
		for i := range proxies {
			if fmt.Sprintf("%d", proxies[i].ID) == ref {
				id := proxies[i].ID
				return &id, nil
			}
		}
	}
	// 名称精确匹配
	for i := range proxies {
		if proxies[i].Name == ref {
			id := proxies[i].ID
			return &id, nil
		}
	}
	// URL 精确匹配（归一化比较）
	target := normalizeGrokProxyURL(ref)
	for i := range proxies {
		if normalizeGrokProxyURL(proxies[i].URL()) == target {
			id := proxies[i].ID
			return &id, nil
		}
	}
	return nil, fmt.Errorf("no active proxy matches %q — create the proxy in sub2api first", ref)
}

func normalizeGrokProxyURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return strings.TrimRight(strings.TrimSpace(raw), "/")
	}
	host := parsed.Host
	if p := parsed.Port(); p == "" || p == "80" || p == "443" {
		if p != "" {
			host = parsed.Hostname()
		}
	}
	scheme := strings.ToLower(parsed.Scheme)
	return scheme + "://" + host
}

func grokSessionAccountType(source string) string {
	if source == "web" {
		return service.AccountTypeGrokWeb
	}
	return service.AccountTypeGrokConsole
}

func normalizeGrokSessionSource(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "grok_web", "web":
		return "web"
	case "grok_console", "console":
		return "console"
	}
	return ""
}

// stripCookieValue 从 "sso" / "sso-rw" 键提取值（部分工具直接平铺 cookie）。
func stripCookieValue(obj map[string]any) string {
	if v := jsonString(obj, "sso"); v != "" {
		return v
	}
	return jsonString(obj, "sso-rw")
}

// extractCFClearance 支持 cloudflare_cookies="cf_clearance=xxx"、
// cloudflare_cookies="xxx"、cf_clearance="xxx" 三种形态。
func extractCFClearance(obj map[string]any) string {
	raw := firstNonEmptyGrok(
		jsonString(obj, "cf_clearance"),
		jsonString(obj, "cloudflare_cookie"),
		jsonString(obj, "cloudflare_cookies"),
	)
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	if idx := strings.Index(lower, "cf_clearance="); idx >= 0 {
		value := raw[idx+len("cf_clearance="):]
		if end := strings.IndexAny(value, "; \t\r\n"); end >= 0 {
			value = value[:end]
		}
		return strings.TrimSpace(value)
	}
	return raw
}

func jsonString(obj map[string]any, key string) string {
	v, _ := obj[key].(string)
	return strings.TrimSpace(v)
}

func jsonInt64(obj map[string]any, key string) int64 {
	switch v := obj[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

func firstNonEmptyGrok(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
