package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// TestChecksumRulesMatchRunnerAlgorithm 锁定白名单的口径不变量：
// 每条规则的 fileChecksum 必须等于 runner 真实算法（strings.TrimSpace 后 SHA256）
// 对当前嵌入文件算出的值，否则该规则永远无法命中，等于没有保护。
//
// 历史上 9 条规则曾用「文件原始字节」的哈希填写，因与 runner 口径不一致而长期失效，
// 本测试防止再次引入同类回归。
func TestChecksumRulesMatchRunnerAlgorithm(t *testing.T) {
	require.NotEmpty(t, migrationChecksumCompatibilityRules)

	for name, rule := range migrationChecksumCompatibilityRules {
		raw, err := migrations.FS.ReadFile(name)
		require.NoErrorf(t, err, "白名单引用了不存在的迁移文件: %s", name)

		content := strings.TrimSpace(string(raw))
		sum := sha256.Sum256([]byte(content))
		fileChecksum := hex.EncodeToString(sum[:])

		require.Equalf(t, fileChecksum, rule.fileChecksum,
			"规则 %s 的 fileChecksum 与 runner 算法不一致（TrimSpace 后 SHA256），该规则不会生效", name)

		// 每条规则至少要能放行一个数据库历史 checksum，否则规则形同虚设。
		require.NotEmptyf(t, rule.acceptedDBChecksum,
			"规则 %s 没有 acceptedDBChecksum，永远无法放行", name)
	}
}

func TestIsMigrationChecksumCompatible(t *testing.T) {
	t.Run("054历史checksum可兼容", func(t *testing.T) {
		ok := isMigrationChecksumCompatible(
			"054_drop_legacy_cache_columns.sql",
			"182c193f3359946cf094090cd9e57d5c3fd9abaffbc1e8fc378646b8a6fa12b4",
			"82de761156e03876653e7a6a4eee883cd927847036f779b0b9f34c42a8af7a7d",
		)
		require.True(t, ok)
	})

	t.Run("054在未知文件checksum下不兼容", func(t *testing.T) {
		ok := isMigrationChecksumCompatible(
			"054_drop_legacy_cache_columns.sql",
			"182c193f3359946cf094090cd9e57d5c3fd9abaffbc1e8fc378646b8a6fa12b4",
			"0000000000000000000000000000000000000000000000000000000000000000",
		)
		require.False(t, ok)
	})

	t.Run("061历史checksum可兼容", func(t *testing.T) {
		ok := isMigrationChecksumCompatible(
			"061_add_usage_log_request_type.sql",
			"08a248652cbab7cfde147fc6ef8cda464f2477674e20b718312faa252e0481c0",
			"66207e7aa5dd0429c2e2c0fabdaf79783ff157fa0af2e81adff2ee03790ec65c",
		)
		require.True(t, ok)
	})

	t.Run("061第二个历史checksum可兼容", func(t *testing.T) {
		ok := isMigrationChecksumCompatible(
			"061_add_usage_log_request_type.sql",
			"222b4a09c797c22e5922b6b172327c824f5463aaa8760e4f621bc5c22e2be0f3",
			"66207e7aa5dd0429c2e2c0fabdaf79783ff157fa0af2e81adff2ee03790ec65c",
		)
		require.True(t, ok)
	})

	t.Run("非白名单迁移不兼容", func(t *testing.T) {
		ok := isMigrationChecksumCompatible(
			"001_init.sql",
			"182c193f3359946cf094090cd9e57d5c3fd9abaffbc1e8fc378646b8a6fa12b4",
			"82de761156e03876653e7a6a4eee883cd927847036f779b0b9f34c42a8af7a7d",
		)
		require.False(t, ok)
	})

	t.Run("109历史checksum可兼容", func(t *testing.T) {
		ok := isMigrationChecksumCompatible(
			"109_auth_identity_compat_backfill.sql",
			"551e498aa5616d2d91096e9d72cf9fb36e418ee22eacc557f8811cadbc9e20ee",
			"2b380305e73ff0c13aa8c811e45897f2b36ca4a438f7b3e8f98e19ecb6bae0b3",
		)
		require.True(t, ok)
	})

	t.Run("109前一版本trim checksum可兼容", func(t *testing.T) {
		ok := isMigrationChecksumCompatible(
			"109_auth_identity_compat_backfill.sql",
			"748ddcdc60f93a1ac562ce8a66ee870f64ee594bf6dbedad55ed8baf3c75b28c",
			"2b380305e73ff0c13aa8c811e45897f2b36ca4a438f7b3e8f98e19ecb6bae0b3",
		)
		require.True(t, ok)
	})

	t.Run("109当前checksum可兼容历史checksum", func(t *testing.T) {
		ok := isMigrationChecksumCompatible(
			"109_auth_identity_compat_backfill.sql",
			"551e498aa5616d2d91096e9d72cf9fb36e418ee22eacc557f8811cadbc9e20ee",
			"2b380305e73ff0c13aa8c811e45897f2b36ca4a438f7b3e8f98e19ecb6bae0b3",
		)
		require.True(t, ok)
	})

	t.Run("109已应用旧版本时仍兼容当前文件", func(t *testing.T) {
		// DB 里是旧修订的 trim checksum，当前文件是另一修订：两者都在已知集合内，应放行。
		ok := isMigrationChecksumCompatible(
			"109_auth_identity_compat_backfill.sql",
			"748ddcdc60f93a1ac562ce8a66ee870f64ee594bf6dbedad55ed8baf3c75b28c",
			"2b380305e73ff0c13aa8c811e45897f2b36ca4a438f7b3e8f98e19ecb6bae0b3",
		)
		require.True(t, ok)
	})

	t.Run("109原始字节口径的raw checksum不再放行", func(t *testing.T) {
		// raw 哈希是历史上误填的口径，任何真实库都不会写入，必须拒绝以免放宽白名单。
		ok := isMigrationChecksumCompatible(
			"109_auth_identity_compat_backfill.sql",
			"0580b4602d85435edf9aca1633db580bb3932f26517f75134106f80275ec2ace",
			"551e498aa5616d2d91096e9d72cf9fb36e418ee22eacc557f8811cadbc9e20ee",
		)
		require.False(t, ok)
	})

	t.Run("110历史checksum可兼容", func(t *testing.T) {
		ok := isMigrationChecksumCompatible(
			"110_pending_auth_and_provider_default_grants.sql",
			"e3d1f433be2b564cfbdc549adf98fce13c5c7b363ebc20fd05b765d0563b0925",
			"57a196a9810fb478fa001dfff110f5c76a7d87fb04f15e12e513fcb75402d7a6",
		)
		require.True(t, ok)
	})

	t.Run("112历史checksum可兼容", func(t *testing.T) {
		ok := isMigrationChecksumCompatible(
			"112_add_payment_order_provider_key_snapshot.sql",
			"ffd3e8a2c9295fa9cbefefd629a78268877e5b51bc970a82d9b3f46ec4ebd15e",
			"ab871fc02da1eabe0de6ca74a119ee3cea9c727caed30af2ae07a0cd1176d1b8",
		)
		require.True(t, ok)
	})

	t.Run("115历史checksum可兼容修复后的legacy external backfill", func(t *testing.T) {
		ok := isMigrationChecksumCompatible(
			"115_auth_identity_legacy_external_backfill.sql",
			"4cf39e508be9fd1a5aa41610cbbebeb80385c9adda45bf78a706de9db4f1385f",
			"022aadd97bb53e755f0cf7a3a957e0cb1a1353b0c39ec4de3234acd2871fd04f",
		)
		require.True(t, ok)
	})

	t.Run("116历史checksum可兼容修复后的legacy external safety reports", func(t *testing.T) {
		ok := isMigrationChecksumCompatible(
			"116_auth_identity_legacy_external_safety_reports.sql",
			"f7757bd929ac67ffb08ce69fa4cf20fad39dbff9d5a5085fb2adabb7607e5877",
			"07edb09fa8d04ffb172b0621e3c22f4d1757d20a24ae267b3b36b087ab72d488",
		)
		require.True(t, ok)
	})

	t.Run("119历史checksum可兼容占位文件", func(t *testing.T) {
		ok := isMigrationChecksumCompatible(
			"119_enforce_payment_orders_out_trade_no_unique.sql",
			"ebd2c67cce0116393fb4f1b5d5116a67c6aceb73820dfb5133d1ff6f36d72d34",
			"0bbe809ae48a9d811dabda1ba1c74955bd71c4a9cc610f9128816818dfa6c11e",
		)
		require.True(t, ok)
	})

	t.Run("118多个历史checksum都可兼容当前版本", func(t *testing.T) {
		for _, dbChecksum := range []string{
			"a38243ca0a72c3a01c0a92b7986423054d6133c0399441f853b99802852720fb",
			"e0cdf835d6c688d64100f483d31bc02ac9ebad414bf1837af239a84bf75b8227",
			"b4a5b7a28f6a7ac67aad214645761e5a8486c83f0f2a1a874d7f67085f83159b",
			"6395ad255f2be2219ad85813b72db6fa7783c81d747e42e098847ef3594f1674",
		} {
			ok := isMigrationChecksumCompatible(
				"118_wechat_dual_mode_and_auth_source_defaults.sql",
				dbChecksum,
				"ed272e0840730b6b8e7838513c4cc8817e8b5e488e27c88b5421adbece5e89c9",
			)
			require.True(t, ok)
		}
	})

	t.Run("120多个历史checksum都可兼容新的notx修复版本", func(t *testing.T) {
		for _, dbChecksum := range []string{
			"e77921f79d539bc24575cb9c16cbe566d2b23ce816190343d0a7568f6a3fcf61",
			"707431450603e70a43ce9fbd61e0c12fa67da4875158ccefabacea069587ab22",
			"04b082b5a239c525154fe9185d324ee2b05ff90da9297e10dba19f9be79aa59a",
			"79ea6127a22e61b3bad6ea29347a8cc3ff005f8b486ef4a51bd04fdda906f931",
		} {
			ok := isMigrationChecksumCompatible(
				"120_enforce_payment_orders_out_trade_no_unique_notx.sql",
				dbChecksum,
				"34aadc0db59a4e390f92a12b73bd74642d9724f33124f73638ae00089ea5e074",
			)
			require.True(t, ok)
		}
	})

	t.Run("119未知checksum不兼容", func(t *testing.T) {
		ok := isMigrationChecksumCompatible(
			"119_enforce_payment_orders_out_trade_no_unique.sql",
			"ebd2c67cce0116393fb4f1b5d5116a67c6aceb73820dfb5133d1ff6f36d72d34",
			"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		)
		require.False(t, ok)
	})
}
