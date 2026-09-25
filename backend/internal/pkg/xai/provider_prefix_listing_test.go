package xai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 列表侧必须隐藏前缀别名：addGrokProviderPrefixedMappings 会为每个 grok 模型
// 额外注入 xai/、x-ai/、grok/ 三种前缀名，若不过滤，同一模型会重复出现 4 次。
func TestHideGrokProviderPrefixedAliases(t *testing.T) {
	in := []string{
		"grok-4.5",
		"grok/grok-4.5",
		"xai/grok-4.5",
		"x-ai/grok-4.5",
		"grok-4.6",
		"grok/grok-4.6",
		"composer-2.5",
		"grok/composer-2.5",
	}
	got := HideGrokProviderPrefixedAliases(in)
	require.Equal(t, []string{"grok-4.5", "grok-4.6", "composer-2.5"}, got)
}

func TestHasGrokProviderPrefix(t *testing.T) {
	for _, m := range []string{"grok/grok-4.5", "xai/grok-4.5", "X-AI/grok-4.5"} {
		require.Truef(t, HasGrokProviderPrefix(m), "%s 应被识别为前缀别名", m)
	}
	for _, m := range []string{"grok-4.5", "composer-2.5", "claude-opus-4-8"} {
		require.Falsef(t, HasGrokProviderPrefix(m), "%s 不应被识别为前缀别名", m)
	}
}

// 去重与空值清理，且保持原有顺序。
func TestHideGrokProviderPrefixedAliasesDedupesAndSkipsEmpty(t *testing.T) {
	got := HideGrokProviderPrefixedAliases([]string{"grok-4.5", "grok-4.5", "", "  ", "grok-4.6"})
	require.Equal(t, []string{"grok-4.5", "grok-4.6"}, got)
}
