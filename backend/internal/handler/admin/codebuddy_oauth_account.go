package admin

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// createCodebuddyAccountParams 建号参数（OAuth 登录与批量导入共用）。
type createCodebuddyAccountParams struct {
	name                 string
	accountType          string
	credentials          map[string]any
	notes                *string
	proxyID              *int64
	concurrency          *int
	priority             *int
	rateMultiplier       *float64
	loadFactor           *int
	groupIDs             []int64
	skipDefaultGroupBind *bool
}

// createCodebuddyAccount 建 CodeBuddy 账号：尽力同步模型目录（CN 动态，失败落静态；
// Global 直接静态），失败不阻断建号。区域由账号类型显式决定。
func (h *CodebuddyOAuthHandler) createCodebuddyAccount(ctx context.Context, params createCodebuddyAccountParams) (*service.Account, error) {
	credentials := params.credentials
	accountType := params.accountType

	concurrency := 3
	if params.concurrency != nil {
		concurrency = *params.concurrency
	}
	priority := 50
	if params.priority != nil {
		priority = *params.priority
	}

	// 模型目录：仅作 /v1/models 展示列表，准入由 IsCodebuddy 区域短路决定，
	// 同步失败不阻断建号（用户可稍后在账号详情手动同步）。
	region := codebuddy.RegionForAccountType(accountType)
	if h.accountTestService != nil {
		if models, modelErr := h.accountTestService.FetchUpstreamSupportedModels(ctx, &service.Account{
			Platform: service.PlatformCodebuddy, Type: accountType, Credentials: credentials,
			ProxyID: params.proxyID, Concurrency: concurrency,
		}); modelErr == nil && len(models) > 0 {
			modelMapping := make(map[string]any, len(models))
			for _, model := range models {
				modelMapping[model] = model
			}
			credentials["model_mapping"] = modelMapping
		} else {
			credentials["model_mapping"] = codebuddyLoginStaticMapping(region)
		}
	} else {
		credentials["model_mapping"] = codebuddyLoginStaticMapping(region)
	}

	extra := map[string]any{}
	if trimmed := trimSpacePointer(params.notes); trimmed != "" {
		extra["notes"] = trimmed
	}

	input := &service.CreateAccountInput{
		Name:                  params.name,
		Notes:                 params.notes,
		Platform:              service.PlatformCodebuddy,
		Type:                  accountType,
		Credentials:           credentials,
		Extra:                 extra,
		ProxyID:               params.proxyID,
		Concurrency:           concurrency,
		Priority:              priority,
		RateMultiplier:        params.rateMultiplier,
		LoadFactor:            params.loadFactor,
		GroupIDs:              params.groupIDs,
		SkipMixedChannelCheck: false,
	}
	if params.skipDefaultGroupBind != nil {
		input.SkipDefaultGroupBind = *params.skipDefaultGroupBind
	}
	return h.adminService.CreateAccount(ctx, input)
}

// codebuddyLoginStaticMapping 静态兜底模型目录（model → model 恒等映射）。
func codebuddyLoginStaticMapping(region codebuddy.Region) map[string]any {
	models := codebuddy.StaticModelIDs(region)
	mapping := make(map[string]any, len(models))
	for _, model := range models {
		mapping[model] = model
	}
	return mapping
}

// trimSpacePointer 解引用并 trim 字符串指针。
func trimSpacePointer(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}
