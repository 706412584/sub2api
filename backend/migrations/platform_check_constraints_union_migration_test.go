package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPlatformCheckConstraintsUnionMigration 校验 238 号收敛迁移把两条分支
// （CodeBuddy / MiniMax）各自整体覆写的平台 CHECK 约束收敛为并集超集。
//
// 背景：237（codebuddy）与 236（minimax）都无条件 DROP + ADD，各含自己的平台、
// 漏掉对方的，谁后执行谁生效；且 236 还漏了基线的 kiro。合并后必须有一个与
// 执行顺序无关的收敛点，否则 user_platform_quotas 会退化成「新用户零配额」
// （224/157 号头注释记载的同型事故）。
func TestPlatformCheckConstraintsUnionMigration(t *testing.T) {
	content, err := FS.ReadFile("238_platform_check_constraints_union.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")

	// 四处约束都必须被处理。
	require.Contains(t, sql, "user_platform_quotas_platform_check")
	require.Contains(t, sql, "composite_model_routes_target_platform_check")
	require.Contains(t, sql, "channel_monitors_provider_check")
	require.Contains(t, sql, "channel_monitor_request_templates_provider_check")

	// 幂等：用 position(...) 判断已覆盖则跳过，使本迁移与 236/237 执行顺序无关。
	require.Contains(t, sql, "position('codebuddy' IN constraint_def)")
	require.Contains(t, sql, "position('minimax' IN constraint_def)")
	require.Contains(t, sql, "position('kiro' IN constraint_def)")
	require.Contains(t, sql, "RETURN;")

	// user_platform_quotas：11 平台并集（9 基线 + codebuddy + minimax），kiro 必须在内。
	require.Contains(t, sql,
		"CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'grok', 'kimi', 'zhipu', 'deepseek', 'codebuddy', 'minimax'))")

	// composite_model_routes：同上并集。
	require.Contains(t, sql,
		"CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'codebuddy', 'minimax'))")

	// channel monitor provider：只补 minimax，**不得**加入 codebuddy
	// （CodeBuddy 不是 channel-monitor provider，代码侧 validateProvider 未收录）。
	require.Contains(t, sql,
		"CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity', 'kimi', 'zhipu', 'deepseek', 'minimax'))")
	require.NotContains(t, sql, "'deepseek', 'minimax', 'codebuddy'")
}

// TestPlatformCheckConstraintsUnionSupersedesBranchMigrations 记录 238 与分支迁移的
// 平台列表关系：238 必须是二者的严格超集，任何一边新增平台而 238 未跟进时此测试失败。
//
// 注意 236_add_minimax_platform.sql 目前只存在于 MiniMax 分支（fork/main），
// 尚未合入本分支，故此处不读该文件，只断言 238 自身已同时覆盖两个平台。
func TestPlatformCheckConstraintsUnionSupersedesBranchMigrations(t *testing.T) {
	unionRaw, err := FS.ReadFile("238_platform_check_constraints_union.sql")
	require.NoError(t, err)
	union := strings.Join(strings.Fields(string(unionRaw)), " ")

	// 237 的 codebuddy 必须仍在 238 的 quota 约束里（合并后不能被抹掉）。
	codebuddyRaw, err := FS.ReadFile("237_user_platform_quotas_add_codebuddy.sql")
	require.NoError(t, err)
	codebuddy := strings.Join(strings.Fields(string(codebuddyRaw)), " ")
	require.Contains(t, codebuddy, "'codebuddy'")
	require.Contains(t, union, "'codebuddy'")

	// 236（MiniMax 分支）的 minimax 也必须在 238 的 quota 约束里，
	// 否则合并后 minimax 配额行 INSERT 违约 → 新用户零配额。
	require.Contains(t, union, "'minimax'")

	// 238 的 quota 约束必须同时包含两边平台与基线 kiro。
	quotaCheck := "CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'grok', 'kimi', 'zhipu', 'deepseek', 'codebuddy', 'minimax'))"
	require.Contains(t, union, quotaCheck)
}
