package service

import (
	"context"
	"strings"
)

// antigravityTierSuffixes 是分档 Gemini 模型的全部档位后缀。
// 上游对这类模型只接受带档位的名字（裸名返回 502）；"tiered" 是上游自身的
// 自动档，参与列表折叠但不参与 effort 选档。
var antigravityTierSuffixes = []string{"low", "medium", "high", "tiered"}

// antigravitySelectableTiers 是能由 reasoning effort 选中的档位。
var antigravitySelectableTiers = []string{"low", "medium", "high"}

func isSelectableAntigravityTier(tier string) bool {
	for _, t := range antigravitySelectableTiers {
		if tier == t {
			return true
		}
	}
	return false
}

// splitAntigravityTierModel 把 gemini-3.8-flash-high 拆成 ("gemini-3.8-flash", "high")。
// 仅识别档位后缀，claude-opus-4-6-thinking / gemini-2.5-flash-lite 这类不受影响。
func splitAntigravityTierModel(model string) (base, tier string) {
	model = strings.ToLower(strings.TrimSpace(model))
	idx := strings.LastIndex(model, "-")
	if idx <= 0 {
		return model, ""
	}
	suffix := model[idx+1:]
	for _, candidate := range antigravityTierSuffixes {
		if suffix == candidate {
			return model[:idx], suffix
		}
	}
	return model, ""
}

// antigravityTierPreference 把 reasoning effort 翻译成候选档位（按优先级降级）。
// 客户端未传 effort 时取最低档；目标档位缺失时优先向低档回退，避免意外升档。
func antigravityTierPreference(effort *string) []string {
	value := ""
	if effort != nil {
		value = NormalizeMaxReasoningEffort(*effort)
	}
	switch value {
	case "medium":
		return []string{"medium", "low", "high"}
	case "high", "xhigh", "max":
		return []string{"high", "medium", "low"}
	default:
		return []string{"low", "medium", "high"}
	}
}

// antigravityTierVariants 收集账号映射目标里 base 的可选档位变体。
// 只看映射的 value（上游模型名），因为最终发给上游的就是 value。
func antigravityTierVariants(mapping map[string]string, base string) map[string]struct{} {
	prefix := base + "-"
	variants := make(map[string]struct{}, len(antigravitySelectableTiers))
	for _, upstream := range mapping {
		upstream = strings.ToLower(strings.TrimSpace(upstream))
		if !strings.HasPrefix(upstream, prefix) {
			continue
		}
		if tier := upstream[len(prefix):]; isSelectableAntigravityTier(tier) {
			variants[tier] = struct{}{}
		}
	}
	return variants
}

// applyAntigravityEffortTier 给裸的分档 Gemini 模型补上档位后缀。
// 客户端请求 gemini-3.8-flash 时按 reasoning effort 选出
// gemini-3.8-flash-{low|medium|high}（未传 effort → 最低档）；
// 已带档位后缀的模型、无档位变体的模型均原样透传。
func applyAntigravityEffortTier(account *Account, mappedModel string, effort *string) string {
	if account == nil || account.Platform != PlatformAntigravity {
		return mappedModel
	}
	base := strings.ToLower(strings.TrimSpace(mappedModel))
	if !strings.HasPrefix(base, "gemini-") {
		return mappedModel
	}
	if _, tier := splitAntigravityTierModel(base); tier != "" {
		return mappedModel
	}
	variants := antigravityTierVariants(account.GetModelMapping(), base)
	if len(variants) == 0 {
		return mappedModel
	}
	for _, tier := range antigravityTierPreference(effort) {
		if _, ok := variants[tier]; ok {
			return base + "-" + tier
		}
	}
	return mappedModel
}

// resolveAntigravityEffortTier 用请求上下文里的 reasoning effort 选档。
func resolveAntigravityEffortTier(ctx context.Context, account *Account, mappedModel string) string {
	return applyAntigravityEffortTier(account, mappedModel, RequestedReasoningEffortFromContext(ctx))
}

// TrimAntigravityTierSuffix 去掉分档 Gemini 模型的档位后缀并返回裸名；
// 不是分档模型时返回空串。供对外模型列表在折叠成裸名后仍认得
// 管理员在分组自定义列表里显式勾选的档位变体。
func TrimAntigravityTierSuffix(model string) string {
	base, tier := splitAntigravityTierModel(model)
	if tier == "" || !strings.HasPrefix(base, "gemini-") {
		return ""
	}
	return base
}

// antigravityTierFamilyRoots 找出会被 applyAntigravityEffortTier 自动选档的基础名：
// 映射里存在裸名透传（base -> base）且存在可选档位变体。只有这些家族的档位变体
// 能安全地从对外模型列表折叠掉——客户端请求裸名即可命中对应档位。
func antigravityTierFamilyRoots(mapping map[string]string) map[string]struct{} {
	roots := make(map[string]struct{})
	for requested, upstream := range mapping {
		requested = strings.ToLower(strings.TrimSpace(requested))
		if !strings.HasPrefix(requested, "gemini-") {
			continue
		}
		if _, tier := splitAntigravityTierModel(requested); tier != "" {
			continue
		}
		if strings.ToLower(strings.TrimSpace(upstream)) != requested {
			continue
		}
		if len(antigravityTierVariants(mapping, requested)) == 0 {
			continue
		}
		roots[requested] = struct{}{}
	}
	return roots
}

// collapseAntigravityTierModels 从对外模型列表里去掉可自动选档的档位变体，只留裸名。
func collapseAntigravityTierModels(models []string, roots map[string]struct{}) []string {
	if len(roots) == 0 || len(models) == 0 {
		return models
	}
	out := make([]string, 0, len(models))
	for _, model := range models {
		if base, tier := splitAntigravityTierModel(model); tier != "" {
			if _, isRoot := roots[base]; isRoot {
				continue
			}
		}
		out = append(out, model)
	}
	return out
}
