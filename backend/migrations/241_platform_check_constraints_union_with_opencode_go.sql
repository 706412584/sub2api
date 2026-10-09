-- 241: 平台白名单 CHECK 约束的最终收敛（238 号两文件并集 + opencode_go）。
--
-- 背景：238 号同号两个文件按文件名排序执行（'o' < 'p'）：
--   238_opencode_go_platform.sql（上游 v0.2.8）：含 opencode_go，原版不含 kiro/codebuddy；
--   238_platform_check_constraints_union.sql（本分支）：含 kiro/codebuddy/minimax，不含 opencode_go。
-- 上游原版已被 238_opencode_go_platform.sql 的并集快照取代，但**已应用过上游原版**的库
-- （其 checksum 被兼容规则放行、不再重跑）会停在「并集缺 opencode_go」的终态：
-- 此时 238 号收敛迁移不会重跑，opencode_go 的配额行/复合路由/渠道监控都会违约。
-- 本迁移是该状态的自愈点：与 238 号两文件的执行顺序无关，幂等，健康库上不做任何表重写。
--
-- 目标列表（与代码权威清单一致）：
--   user_platform_quotas.platform        ← service.AllowedQuotaPlatforms（12：9 基线含 kiro
--                                          + codebuddy + minimax + opencode_go）
--   composite_model_routes.target_platform ← service.isConcreteRequestPlatform（同 12，超集无副作用）
--   channel_monitors.provider /
--   channel_monitor_request_templates.provider ← 不含 kiro/codebuddy（非 monitor provider），
--                                          10 = openai/anthropic/gemini/grok/antigravity
--                                          + kimi/zhipu/deepseek/minimax/opencode_go

-- ── 1. user_platform_quotas.platform ────────────────────────────────────────
DO $$
DECLARE
    constraint_def TEXT;
BEGIN
    SELECT pg_get_constraintdef(c.oid)
      INTO constraint_def
      FROM pg_constraint c
      JOIN pg_class t ON t.oid = c.conrelid
     WHERE t.relname = 'user_platform_quotas'
       AND c.conname = 'user_platform_quotas_platform_check';

    IF constraint_def IS NOT NULL
       AND position('codebuddy' IN constraint_def) > 0
       AND position('minimax' IN constraint_def) > 0
       AND position('kiro' IN constraint_def) > 0
       AND position('opencode_go' IN constraint_def) > 0 THEN
        RETURN;
    END IF;

    ALTER TABLE user_platform_quotas
        DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

    ALTER TABLE user_platform_quotas
        ADD CONSTRAINT user_platform_quotas_platform_check
        CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'grok',
                            'kimi', 'zhipu', 'deepseek', 'codebuddy', 'minimax', 'opencode_go'));
END $$;

-- ── 2. composite_model_routes.target_platform ───────────────────────────────
DO $$
DECLARE
    constraint_def TEXT;
BEGIN
    SELECT pg_get_constraintdef(c.oid)
      INTO constraint_def
      FROM pg_constraint c
      JOIN pg_class t ON t.oid = c.conrelid
     WHERE t.relname = 'composite_model_routes'
       AND c.conname = 'composite_model_routes_target_platform_check';

    IF constraint_def IS NOT NULL
       AND position('codebuddy' IN constraint_def) > 0
       AND position('minimax' IN constraint_def) > 0
       AND position('kiro' IN constraint_def) > 0
       AND position('opencode_go' IN constraint_def) > 0 THEN
        RETURN;
    END IF;

    ALTER TABLE composite_model_routes
        DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;

    ALTER TABLE composite_model_routes
        ADD CONSTRAINT composite_model_routes_target_platform_check
        CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'grok',
                                   'kimi', 'zhipu', 'deepseek', 'codebuddy', 'minimax', 'opencode_go'));
END $$;

-- ── 3. channel_monitors.provider ────────────────────────────────────────────
DO $$
DECLARE
    constraint_def TEXT;
BEGIN
    SELECT pg_get_constraintdef(c.oid)
      INTO constraint_def
      FROM pg_constraint c
      JOIN pg_class t ON t.oid = c.conrelid
     WHERE t.relname = 'channel_monitors'
       AND c.conname = 'channel_monitors_provider_check';

    IF constraint_def IS NOT NULL
       AND position('minimax' IN constraint_def) > 0
       AND position('opencode_go' IN constraint_def) > 0 THEN
        RETURN;
    END IF;

    ALTER TABLE channel_monitors
        DROP CONSTRAINT IF EXISTS channel_monitors_provider_check;

    ALTER TABLE channel_monitors
        ADD CONSTRAINT channel_monitors_provider_check
        CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity',
                            'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'));
END $$;

-- ── 4. channel_monitor_request_templates.provider ───────────────────────────
DO $$
DECLARE
    constraint_def TEXT;
BEGIN
    SELECT pg_get_constraintdef(c.oid)
      INTO constraint_def
      FROM pg_constraint c
      JOIN pg_class t ON t.oid = c.conrelid
     WHERE t.relname = 'channel_monitor_request_templates'
       AND c.conname = 'channel_monitor_request_templates_provider_check';

    IF constraint_def IS NOT NULL
       AND position('minimax' IN constraint_def) > 0
       AND position('opencode_go' IN constraint_def) > 0 THEN
        RETURN;
    END IF;

    ALTER TABLE channel_monitor_request_templates
        DROP CONSTRAINT IF EXISTS channel_monitor_request_templates_provider_check;

    ALTER TABLE channel_monitor_request_templates
        ADD CONSTRAINT channel_monitor_request_templates_provider_check
        CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity',
                            'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'));
END $$;
