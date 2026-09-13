-- 238: 平台白名单 CHECK 约束的合并收敛（CodeBuddy 分支 ∩ MiniMax/IM 分支）。
--
-- 背景：两条分支各自从同一基线（224 号，9 平台含 kiro）独立扩展平台白名单，
-- 于是合并后出现两个问题：
--
--   1. 两边的迁移都**整体覆写** CHECK 列表，各自只包含自己新增的平台：
--        - 237_user_platform_quotas_add_codebuddy.sql  → 含 codebuddy、不含 minimax
--        - 236_add_minimax_platform.sql                → 含 minimax、不含 codebuddy
--      两条迁移都无条件 DROP + ADD，谁后执行谁生效，先执行的那条被静默抹掉。
--      迁移按文件名排序执行，237 > 236，故当前实到状态是「有 codebuddy、无 minimax」。
--   2. 236 号迁移本身还漏掉了基线里的 kiro（224 明确包含），
--      使 Kiro 账号的配额行 INSERT 违约 → 批量 INSERT 整条中止 → 新用户零配额
--      （与 224/157 号头注释记载的同型事故一致）。
--
-- 本迁移把三处约束一次性收敛为**并集超集**，且用 position(...) 判断当前约束是否
-- 已覆盖全部目标平台，已覆盖则完全不动——因此本迁移与 236/237 的执行顺序无关，
-- 也不会在已正确的库上产生无谓的表重写。
--
-- 注意 provider 白名单（channel_monitors / channel_monitor_request_templates）：
--   CodeBuddy 不是 channel-monitor provider（代码侧 validateProvider 未收录），
--   故这两处只补 minimax，**不**加 codebuddy。user_platform_quotas 与
--   composite_model_routes 则按 AllowedQuotaPlatforms / isConcreteRequestPlatform
--   的代码权威清单补齐两平台。

-- ── 1. user_platform_quotas.platform ────────────────────────────────────────
-- 目标：9 基线（anthropic/openai/gemini/antigravity/kiro/grok/kimi/zhipu/deepseek）
--       + codebuddy + minimax = 11 平台。
-- 权威来源：internal/service/domain_constants.go 的 AllowedQuotaPlatforms。
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

    -- 已覆盖全部 11 平台则跳过：不重写约束，避免无谓的表扫描。
    IF constraint_def IS NOT NULL
       AND position('codebuddy' IN constraint_def) > 0
       AND position('minimax' IN constraint_def) > 0
       AND position('kiro' IN constraint_def) > 0 THEN
        RETURN;
    END IF;

    ALTER TABLE user_platform_quotas
        DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

    ALTER TABLE user_platform_quotas
        ADD CONSTRAINT user_platform_quotas_platform_check
        CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'grok',
                            'kimi', 'zhipu', 'deepseek', 'codebuddy', 'minimax'));
END $$;

-- ── 2. composite_model_routes.target_platform ───────────────────────────────
-- 目标：9 基线 + codebuddy + minimax。
-- 权威来源：internal/service/composite_platform.go 的 isConcreteRequestPlatform。
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
       AND position('minimax' IN constraint_def) > 0 THEN
        RETURN;
    END IF;

    ALTER TABLE composite_model_routes
        DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;

    ALTER TABLE composite_model_routes
        ADD CONSTRAINT composite_model_routes_target_platform_check
        CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                                   'kimi', 'zhipu', 'deepseek', 'codebuddy', 'minimax'));
END $$;

-- ── 3. channel_monitors.provider ────────────────────────────────────────────
-- 目标：9 基线 + minimax（不含 codebuddy，见文件头说明）。
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
       AND position('minimax' IN constraint_def) > 0 THEN
        RETURN;
    END IF;

    ALTER TABLE channel_monitors
        DROP CONSTRAINT IF EXISTS channel_monitors_provider_check;

    ALTER TABLE channel_monitors
        ADD CONSTRAINT channel_monitors_provider_check
        CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity',
                            'kimi', 'zhipu', 'deepseek', 'minimax'));
END $$;

-- ── 4. channel_monitor_request_templates.provider ───────────────────────────
-- 目标：同 channel_monitors。
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
       AND position('minimax' IN constraint_def) > 0 THEN
        RETURN;
    END IF;

    ALTER TABLE channel_monitor_request_templates
        DROP CONSTRAINT IF EXISTS channel_monitor_request_templates_provider_check;

    ALTER TABLE channel_monitor_request_templates
        ADD CONSTRAINT channel_monitor_request_templates_provider_check
        CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity',
                            'kimi', 'zhipu', 'deepseek', 'minimax'));
END $$;
