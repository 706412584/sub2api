-- Add OpenCode as a first-class platform (account types Zen / GO).
--
-- 1. user_platform_quotas.platform CHECK
-- 2. composite_model_routes.target_platform CHECK
-- 3. channel_monitors / channel_monitor_request_templates provider CHECK
--
-- Runs after 237_add_minimax_platform.sql. DROP ... IF EXISTS + 幂等守卫保证可重入；
-- 新约束是 237 的超集，必须同时保留 MiniMax。
--
-- fork 合并说明（v0.2.8）：本文件按文件名排序会**先于**同号的
-- 238_platform_check_constraints_union.sql 执行（'o' < 'p'），而后者只在
-- 「codebuddy + minimax + kiro 均已在内」时才跳过。上游原版的两处列表
-- 不含 kiro/codebuddy，因此在已部署 238 收敛迁移的库上会：
--   1. 把约束收窄回「无 kiro/codebuddy」→ 存量 kiro/codebuddy 配额行使
--      ADD CONSTRAINT 校验失败 → 迁移报错 → 启动中止；
--   2. 即便无存量行，也会静默丢掉这两个平台的配额能力。
-- 故此处两处列表取**本分支并集快照**（9 基线含 kiro + codebuddy + minimax
-- + opencode_go = 12 平台），使本迁移自身不窄化约束、且与执行顺序无关。
-- 权威来源：internal/service/domain_constants.go 的 AllowedQuotaPlatforms
-- 与 internal/service/composite_platform.go 的 isConcreteRequestPlatform。

ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'grok',
                        'kimi', 'zhipu', 'deepseek', 'codebuddy', 'minimax', 'opencode_go'));

ALTER TABLE composite_model_routes
    DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;

ALTER TABLE composite_model_routes
    ADD CONSTRAINT composite_model_routes_target_platform_check
    CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'grok',
                               'kimi', 'zhipu', 'deepseek', 'codebuddy', 'minimax', 'opencode_go'));

DO $$
DECLARE
    monitor_constraint_def TEXT;
    template_constraint_def TEXT;
BEGIN
    SELECT pg_get_constraintdef(c.oid)
      INTO monitor_constraint_def
      FROM pg_constraint c
      JOIN pg_class t ON t.oid = c.conrelid
     WHERE t.relname = 'channel_monitors'
       AND c.conname = 'channel_monitors_provider_check';

    IF monitor_constraint_def IS NULL OR position('opencode_go' IN monitor_constraint_def) = 0 THEN
        ALTER TABLE channel_monitors
            DROP CONSTRAINT IF EXISTS channel_monitors_provider_check;
        ALTER TABLE channel_monitors
            ADD CONSTRAINT channel_monitors_provider_check
            CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok',
                                'antigravity', 'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'));
    END IF;

    SELECT pg_get_constraintdef(c.oid)
      INTO template_constraint_def
      FROM pg_constraint c
      JOIN pg_class t ON t.oid = c.conrelid
     WHERE t.relname = 'channel_monitor_request_templates'
       AND c.conname = 'channel_monitor_request_templates_provider_check';

    IF template_constraint_def IS NULL OR position('opencode_go' IN template_constraint_def) = 0 THEN
        ALTER TABLE channel_monitor_request_templates
            DROP CONSTRAINT IF EXISTS channel_monitor_request_templates_provider_check;
        ALTER TABLE channel_monitor_request_templates
            ADD CONSTRAINT channel_monitor_request_templates_provider_check
            CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok',
                                'antigravity', 'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'));
    END IF;
END $$;
