package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMiniMaxPlatformMigration(t *testing.T) {
	content, err := FS.ReadFile("236_add_minimax_platform.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")

	// 本迁移重建 CHECK 时会整块覆盖 224 的结果，必须保留 224 已加入的 kiro，
	// 否则库中存在 kiro 配额行时 ADD CONSTRAINT 校验失败、迁移中断、应用启动崩溃。
	require.Contains(t, sql,
		"CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax'))")
	require.Contains(t, sql,
		"CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax'))")
	// channel_monitors 的 provider 白名单不含 kiro/codebuddy（二者不是监控 provider），此处保持 9 平台。
	require.Contains(t, sql,
		"CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity', 'kimi', 'zhipu', 'deepseek', 'minimax'))")

	// 配额/路由列表必须是 224 的超集：224 有的平台一个都不能少。
	for _, platform := range []string{"anthropic", "openai", "gemini", "antigravity", "kiro", "grok", "kimi", "zhipu", "deepseek", "minimax"} {
		require.Containsf(t, sql, "'"+platform+"'", "236 缺少 224 已有的平台 %s", platform)
	}
}
