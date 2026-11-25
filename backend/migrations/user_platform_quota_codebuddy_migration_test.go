package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUserPlatformQuotasCodebuddyMigration 校验 237 号迁移把 codebuddy 加入
// user_platform_quotas.platform 的 CHECK 约束（对照 157 号 grok、224 号国产供应商）。
// 约束未放宽时，注册预填充 10 平台默认配额会整条 INSERT 中止 → 新用户零配额行
// （缺失配额行 = 无限额），管理端设置 CodeBuddy 平台配额直接 500。
func TestUserPlatformQuotasCodebuddyMigration(t *testing.T) {
	content, err := FS.ReadFile("237_user_platform_quotas_add_codebuddy.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check")
	require.Contains(t, sql,
		"CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'grok', 'kimi', 'zhipu', 'deepseek', 'codebuddy'))")
}
