//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func antigravityTierAccount(mapping map[string]string) *Account {
	raw := make(map[string]any, len(mapping))
	for k, v := range mapping {
		raw[k] = v
	}
	return &Account{
		ID:          1,
		Platform:    PlatformAntigravity,
		Credentials: map[string]any{"model_mapping": raw},
	}
}

var antigravityTierTestMapping = map[string]string{
	"gemini-3.8-flash":        "gemini-3.8-flash",
	"gemini-3.8-flash-low":    "gemini-3.8-flash-low",
	"gemini-3.8-flash-medium": "gemini-3.8-flash-medium",
	"gemini-3.8-flash-high":   "gemini-3.8-flash-high",
	"gemini-3.8-flash-tiered": "gemini-3.8-flash-tiered",
	"claude-opus-4-6":         "claude-opus-4-6-thinking",
	"gemini-2.5-flash-lite":   "gemini-2.5-flash-lite",
}

// 客户端只请求裸名 gemini-3.8-flash，档位由 reasoning effort 决定；未传 effort 走最低档。
func TestApplyAntigravityEffortTier(t *testing.T) {
	account := antigravityTierAccount(antigravityTierTestMapping)
	effort := func(v string) *string { return &v }

	tests := []struct {
		name   string
		model  string
		effort *string
		want   string
	}{
		{"未传 effort 用最低档", "gemini-3.8-flash", nil, "gemini-3.8-flash-low"},
		{"minimal 归到最低档", "gemini-3.8-flash", effort("minimal"), "gemini-3.8-flash-low"},
		{"low", "gemini-3.8-flash", effort("low"), "gemini-3.8-flash-low"},
		{"medium", "gemini-3.8-flash", effort("medium"), "gemini-3.8-flash-medium"},
		{"high", "gemini-3.8-flash", effort("high"), "gemini-3.8-flash-high"},
		{"xhigh 收敛到 high", "gemini-3.8-flash", effort("xhigh"), "gemini-3.8-flash-high"},
		{"max 收敛到 high", "gemini-3.8-flash", effort("max"), "gemini-3.8-flash-high"},
		{"未知 effort 走最低档", "gemini-3.8-flash", effort("bogus"), "gemini-3.8-flash-low"},
		{"已带档位后缀原样透传", "gemini-3.8-flash-medium", effort("high"), "gemini-3.8-flash-medium"},
		{"上游自动档 tiered 不重选", "gemini-3.8-flash-tiered", effort("high"), "gemini-3.8-flash-tiered"},
		{"无档位变体的模型不动", "gemini-2.5-flash-lite", effort("high"), "gemini-2.5-flash-lite"},
		{"claude 模型不受影响", "claude-opus-4-6-thinking", effort("high"), "claude-opus-4-6-thinking"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, applyAntigravityEffortTier(account, tt.model, tt.effort))
		})
	}
}

// 缺档时向低档回退，不意外升档。
// 用 3.9（不在 ensureAntigravityDefaultPassthroughs 清单里）构造真正的缺档场景，
// 3.6/3.8 会被默认透传补齐全档位，无法体现回退。
func TestApplyAntigravityEffortTier_MissingTierFallsBackDown(t *testing.T) {
	account := antigravityTierAccount(map[string]string{
		"gemini-3.9-flash":      "gemini-3.9-flash",
		"gemini-3.9-flash-low":  "gemini-3.9-flash-low",
		"gemini-3.9-flash-high": "gemini-3.9-flash-high",
	})
	medium := "medium"
	require.Equal(t, "gemini-3.9-flash-low", applyAntigravityEffortTier(account, "gemini-3.9-flash", &medium),
		"medium 缺失时回退 low 而不是升到 high")

	onlyHigh := antigravityTierAccount(map[string]string{
		"gemini-3.9-flash":      "gemini-3.9-flash",
		"gemini-3.9-flash-high": "gemini-3.9-flash-high",
	})
	require.Equal(t, "gemini-3.9-flash-high", applyAntigravityEffortTier(onlyHigh, "gemini-3.9-flash", nil),
		"只有 high 一档时别无选择")
}

// 非 Antigravity 平台不干预。
func TestApplyAntigravityEffortTier_OtherPlatformsUntouched(t *testing.T) {
	account := antigravityTierAccount(antigravityTierTestMapping)
	account.Platform = PlatformGemini
	require.Equal(t, "gemini-3.8-flash", applyAntigravityEffortTier(account, "gemini-3.8-flash", nil))
	require.Equal(t, "gemini-3.8-flash", applyAntigravityEffortTier(nil, "gemini-3.8-flash", nil))
}

// 上游按账号灰度开放档位：只有 tiered（上游自动档）没有 low/medium/high 时，
// 裸名落到 tiered 让上游选档，而不是把裸名原样发出去吃 404（实测 9300 账号 3.8）。
func TestApplyAntigravityEffortTier_TieredOnlyFallsBackToTiered(t *testing.T) {
	account := antigravityTierAccount(map[string]string{
		"gemini-3.8-flash":        "gemini-3.8-flash",
		"gemini-3.8-flash-tiered": "gemini-3.8-flash-tiered",
	})
	high := "high"
	require.Equal(t, "gemini-3.8-flash-tiered", applyAntigravityEffortTier(account, "gemini-3.8-flash", nil))
	require.Equal(t, "gemini-3.8-flash-tiered", applyAntigravityEffortTier(account, "gemini-3.8-flash", &high))

	// 连 tiered 都没有的裸名仍原样透传（由映射表决定是否有这个模型）
	bare := antigravityTierAccount(map[string]string{"gemini-3.9-flash": "gemini-3.9-flash"})
	require.Equal(t, "gemini-3.9-flash", applyAntigravityEffortTier(bare, "gemini-3.9-flash", nil))
}

// resolveFinalAntigravityModelKey 的限流 key 必须与实际转发模型一致，
// 否则档位模型的限流状态会记到裸名上。
func TestResolveFinalAntigravityModelKey_AppliesEffortTier(t *testing.T) {
	account := antigravityTierAccount(antigravityTierTestMapping)

	require.Equal(t, "gemini-3.8-flash-low",
		resolveFinalAntigravityModelKey(context.Background(), account, "gemini-3.8-flash"))

	ctx := WithRequestedReasoningEffort(context.Background(), "high")
	require.Equal(t, "gemini-3.8-flash-high",
		resolveFinalAntigravityModelKey(ctx, account, "gemini-3.8-flash"))

	// thinking 后缀路径不受影响
	require.Equal(t, "claude-opus-4-6-thinking",
		resolveFinalAntigravityModelKey(context.Background(), account, "claude-opus-4-6"))
}

// 对外模型列表只保留可自动选档家族的裸名。
func TestCollapseAntigravityTierModels(t *testing.T) {
	roots := antigravityTierFamilyRoots(antigravityTierTestMapping)
	require.Equal(t, map[string]struct{}{"gemini-3.8-flash": {}}, roots)

	got := collapseAntigravityTierModels([]string{
		"claude-opus-4-6",
		"gemini-2.5-flash-lite",
		"gemini-3.1-pro-high",
		"gemini-3.1-pro-low",
		"gemini-3.8-flash",
		"gemini-3.8-flash-high",
		"gemini-3.8-flash-low",
		"gemini-3.8-flash-medium",
		"gemini-3.8-flash-tiered",
	}, roots)

	require.Equal(t, []string{
		"claude-opus-4-6",
		"gemini-2.5-flash-lite",
		"gemini-3.1-pro-high",
		"gemini-3.1-pro-low",
		"gemini-3.8-flash",
	}, got, "3.1-pro 无裸名透传，档位变体必须保留")
}

// 裸名映射到别的上游模型时不算自动选档家族，档位变体不能被折叠掉。
func TestAntigravityTierFamilyRoots_RequiresPassthroughBase(t *testing.T) {
	roots := antigravityTierFamilyRoots(map[string]string{
		"gemini-3.1-pro":      "gemini-pro-agent",
		"gemini-3.1-pro-low":  "gemini-3.1-pro-low",
		"gemini-3.1-pro-high": "gemini-pro-agent",
	})
	require.Empty(t, roots)

	models := []string{"gemini-3.1-pro", "gemini-3.1-pro-low", "gemini-3.1-pro-high"}
	require.Equal(t, models, collapseAntigravityTierModels(models, roots))
}

// 分组自定义模型列表勾选了档位变体时，需要能回落到裸名做白名单校验。
func TestTrimAntigravityTierSuffix(t *testing.T) {
	require.Equal(t, "gemini-3.8-flash", TrimAntigravityTierSuffix("gemini-3.8-flash-high"))
	require.Equal(t, "gemini-3.8-flash", TrimAntigravityTierSuffix("gemini-3.8-flash-tiered"))
	require.Equal(t, "", TrimAntigravityTierSuffix("gemini-3.8-flash"))
	require.Equal(t, "", TrimAntigravityTierSuffix("gemini-2.5-flash-lite"))
	require.Equal(t, "", TrimAntigravityTierSuffix("claude-opus-4-6-thinking"))
	require.Equal(t, "", TrimAntigravityTierSuffix("gpt-oss-120b-medium"), "非 gemini 前缀不参与")
}
