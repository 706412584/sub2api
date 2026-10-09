package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// CodeBuddy 批量导入：解析 workbuddy2api 的 auths/*.json（嵌套形/扁平形），
// 兼容单对象、对象数组与文件内容粘贴。区域由请求参数显式指定（不靠 domain 隐式推导），
// 与账号类型绑定——这是本仓库有意偏离 workbuddy2api 的设计决策（建号即选区）。
type CodebuddyImportRequest struct {
	Data                 any      `json:"data"`
	Region               string   `json:"region"` // "cn"（缺省）或 "global"
	Name                 string   `json:"name"`
	Notes                string   `json:"notes"`
	GroupIDs             []int64  `json:"group_ids"`
	ProxyID              *int64   `json:"proxy_id"`
	Concurrency          *int     `json:"concurrency"`
	Priority             *int     `json:"priority"`
	RateMultiplier       *float64 `json:"rate_multiplier"`
	LoadFactor           *int     `json:"load_factor"`
	SkipDefaultGroupBind *bool    `json:"skip_default_group_bind"`
}

type CodebuddyImportResult struct {
	Total   int                      `json:"total"`
	Created int                      `json:"created"`
	Failed  int                      `json:"failed"`
	Items   []CodebuddyImportItem    `json:"items,omitempty"`
	Errors  []CodebuddyImportMessage `json:"errors,omitempty"`
}

type CodebuddyImportItem struct {
	Index     int    `json:"index"`
	Name      string `json:"name,omitempty"`
	Action    string `json:"action"`
	AccountID int64  `json:"account_id,omitempty"`
	Message   string `json:"message,omitempty"`
}

type CodebuddyImportMessage struct {
	Index   int    `json:"index"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
}

// ImportCodebuddyAccounts 导入 CodeBuddy 账号（批量）。
// POST /api/v1/admin/accounts/import/codebuddy
func (h *AccountHandler) ImportCodebuddyAccounts(c *gin.Context) {
	var req CodebuddyImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.Data == nil {
		response.BadRequest(c, "data field is required")
		return
	}

	accounts, err := parseCodebuddyImportData(req.Data)
	if err != nil {
		response.BadRequest(c, "Failed to parse CodeBuddy auth data: "+err.Error())
		return
	}
	if len(accounts) == 0 {
		response.BadRequest(c, "No valid CodeBuddy accounts found in the input data")
		return
	}

	executeAdminIdempotentJSON(c, "admin.accounts.import_codebuddy", req, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		return h.importCodebuddyAccounts(ctx, req, accounts)
	})
}

// parseCodebuddyImportData 解析导入数据为凭证列表。
// 支持：单对象、对象数组、{"accounts": [...]} 包装、raw JSON 文本字符串。
func parseCodebuddyImportData(data any) ([]codebuddy.Credentials, error) {
	var entries []any
	switch typed := data.(type) {
	case []any:
		entries = typed
	case map[string]any:
		if wrapped, ok := typed["accounts"].([]any); ok && len(typed) == 1 {
			entries = wrapped
		} else {
			entries = []any{typed}
		}
	case string:
		raw := strings.TrimSpace(typed)
		if raw == "" {
			return nil, fmt.Errorf("empty data")
		}
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			// 非数组：按单对象解析
			var obj map[string]any
			if json.Unmarshal([]byte(raw), &obj) == nil {
				entries = []any{obj}
			} else {
				return nil, fmt.Errorf("data is neither a JSON array nor a JSON object")
			}
		}
	default:
		return nil, fmt.Errorf("unsupported data type %T", data)
	}

	accounts := make([]codebuddy.Credentials, 0, len(entries))
	for i, entry := range entries {
		obj, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("entry %d is not a JSON object", i)
		}
		creds, err := codebuddy.ParseAuthFile(obj)
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}
		accounts = append(accounts, creds)
	}
	return accounts, nil
}

// importCodebuddyAccounts 逐个建号。模型同步失败落静态表，不阻断导入。
func (h *AccountHandler) importCodebuddyAccounts(ctx context.Context, req CodebuddyImportRequest, accounts []codebuddy.Credentials) (CodebuddyImportResult, error) {
	result := CodebuddyImportResult{
		Total: len(accounts),
		Items: make([]CodebuddyImportItem, 0, len(accounts)),
	}

	accountType := service.AccountTypeCodebuddyCN
	if strings.EqualFold(strings.TrimSpace(req.Region), string(codebuddy.RegionGlobal)) {
		accountType = service.AccountTypeCodebuddyGlob
	}
	region := codebuddy.RegionForAccountType(accountType)

	concurrency := 3
	if req.Concurrency != nil {
		concurrency = *req.Concurrency
	}
	priority := 50
	if req.Priority != nil {
		priority = *req.Priority
	}

	for i, account := range accounts {
		item := CodebuddyImportItem{Index: i}
		accountName := buildCodebuddyImportAccountName(req.Name, account, i)

		credentials := account.ToCredentialsMap()
		credentials["model_mapping"] = codebuddyLoginStaticMapping(region)
		if h.accountTestService != nil {
			if models, modelErr := h.accountTestService.FetchUpstreamSupportedModels(ctx, &service.Account{
				Platform: service.PlatformCodebuddy, Type: accountType, Credentials: credentials,
				ProxyID: req.ProxyID, Concurrency: concurrency,
			}); modelErr == nil && len(models) > 0 {
				modelMapping := make(map[string]any, len(models))
				for _, model := range models {
					modelMapping[model] = model
				}
				credentials["model_mapping"] = modelMapping
			}
		}

		extra := map[string]any{}
		if strings.TrimSpace(req.Notes) != "" {
			extra["notes"] = strings.TrimSpace(req.Notes)
		}

		createReq := &service.CreateAccountInput{
			Name:        accountName,
			Platform:    service.PlatformCodebuddy,
			Type:        accountType,
			Credentials: credentials,
			Extra:       extra,
			Concurrency: concurrency,
			Priority:    priority,
		}
		if req.ProxyID != nil {
			createReq.ProxyID = req.ProxyID
		}
		if req.RateMultiplier != nil {
			createReq.RateMultiplier = req.RateMultiplier
		}
		if req.LoadFactor != nil {
			createReq.LoadFactor = req.LoadFactor
		}
		if req.SkipDefaultGroupBind != nil {
			createReq.SkipDefaultGroupBind = *req.SkipDefaultGroupBind
		}
		if len(req.GroupIDs) > 0 {
			createReq.GroupIDs = req.GroupIDs
		}

		item.Name = accountName
		created, err := h.adminService.CreateAccount(ctx, createReq)
		if err != nil {
			result.Failed++
			item.Action = "failed"
			item.Message = err.Error()
			result.Errors = append(result.Errors, CodebuddyImportMessage{Index: i, Name: accountName, Message: err.Error()})
		} else {
			result.Created++
			item.Action = "created"
			item.AccountID = created.ID
		}
		result.Items = append(result.Items, item)
	}

	return result, nil
}

// buildCodebuddyImportAccountName 生成导入账号名：
// 批量前缀-N > nickname > uid > codebuddy-{region}-N。
func buildCodebuddyImportAccountName(prefix string, account codebuddy.Credentials, index int) string {
	if strings.TrimSpace(prefix) != "" {
		return fmt.Sprintf("%s-%d", strings.TrimSpace(prefix), index+1)
	}
	for _, candidate := range []string{account.Nickname, account.UID} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return fmt.Sprintf("codebuddy-account-%d", index+1)
}
