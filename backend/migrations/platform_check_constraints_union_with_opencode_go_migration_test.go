package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPlatformCheckConstraintsUnionWithOpenCodeGoMigration 校验 241 号收敛迁移
// 把平台 CHECK 约束收敛为「238 两文件的并集 + opencode_go」终态。
//
// 背景：238 号同号两文件按文件名排序执行（'o' < 'p'），上游原版
// 238_opencode_go_platform.sql 不含 kiro/codebuddy，本分支
// 238_platform_check_constraints_union.sql 不含 opencode_go。已应用过上游原版的库
// 因 checksum 兼容规则不再重跑该文件，会停在「并集缺 opencode_go」的终态，
// 需要本迁移自愈。
func TestPlatformCheckConstraintsUnionWithOpenCodeGoMigration(t *testing.T) {
	content, err := FS.ReadFile("241_platform_check_constraints_union_with_opencode_go.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")

	// 四处约束都必须被处理。
	require.Contains(t, sql, "user_platform_quotas_platform_check")
	require.Contains(t, sql, "composite_model_routes_target_platform_check")
	require.Contains(t, sql, "channel_monitors_provider_check")
	require.Contains(t, sql, "channel_monitor_request_templates_provider_check")

	// 幂等：已覆盖全部目标平台则跳过，RETURN 不动约束。
	require.Contains(t, sql, "position('opencode_go' IN constraint_def)")
	require.Contains(t, sql, "position('codebuddy' IN constraint_def)")
	require.Contains(t, sql, "position('kiro' IN constraint_def)")
	require.Contains(t, sql, "RETURN;")
	require.NotContains(t, sql, "ALTER COLUMN")

	// user_platform_quotas / composite_model_routes：12 平台并集（与 AllowedQuotaPlatforms 一致）。
	require.Contains(t, sql,
		"CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'grok', 'kimi', 'zhipu', 'deepseek', 'codebuddy', 'minimax', 'opencode_go'))")
	require.Contains(t, sql,
		"CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'grok', 'kimi', 'zhipu', 'deepseek', 'codebuddy', 'minimax', 'opencode_go'))")

	// channel monitor provider：minimax + opencode_go，**不含** kiro/codebuddy
	// （两者都不是 channel-monitor provider，代码侧 monitorProviders 未收录）。
	require.Contains(t, sql,
		"CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity', 'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'))")
	require.NotContains(t, sql, "'minimax', 'codebuddy'")

	// 与 238 号两文件的关系：241 的 quota 列表必须是二者的严格超集。
	opencodeRaw, err := FS.ReadFile("238_opencode_go_platform.sql")
	require.NoError(t, err)
	opencode := strings.Join(strings.Fields(string(opencodeRaw)), " ")
	unionRaw, err := FS.ReadFile("238_platform_check_constraints_union.sql")
	require.NoError(t, err)
	union := strings.Join(strings.Fields(string(unionRaw)), " ")

	require.Contains(t, opencode, "'opencode_go'")
	require.Contains(t, union, "'codebuddy'")
	require.Contains(t, union, "'minimax'")
	require.Contains(t, sql, "'codebuddy'")
	require.Contains(t, sql, "'minimax'")
	require.Contains(t, sql, "'opencode_go'")
	require.Contains(t, sql, "'kiro'")
}

// TestOpenCodeGoPlatformMigrationKeepsForkUnion 记录 238_opencode_go_platform.sql
// 已按本分支并集快照改写：它先于 238 号收敛迁移执行，若自身收窄约束会抹掉
// kiro/codebuddy（存量行还会让 ADD CONSTRAINT 校验失败，迁移报错 → 启动中止）。
func TestOpenCodeGoPlatformMigrationKeepsForkUnion(t *testing.T) {
	content, err := FS.ReadFile("238_opencode_go_platform.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")

	require.Contains(t, sql, "'kiro'")
	require.Contains(t, sql, "'codebuddy'")
	require.Contains(t, sql, "'minimax'")
	require.Contains(t, sql, "'opencode_go'")

	// provider 两处仍不含 codebuddy（非 monitor provider），但必须含 kiro 之外的基线。
	require.Contains(t, sql,
		"CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity', 'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'))")
	require.NotContains(t, sql, "'minimax', 'codebuddy'")
}
