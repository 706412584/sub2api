-- IM chat bots: platform adapters bound to existing API keys.
-- Phase 1 platforms: telegram, feishu. Reserved: dingtalk, wecom, qq, slack, wechat, whatsapp.

CREATE TABLE IF NOT EXISTS im_bots (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    platform VARCHAR(32) NOT NULL,
    credentials_encrypted TEXT NOT NULL,
    api_key_id BIGINT NOT NULL,
    model_override VARCHAR(200) NOT NULL DEFAULT '',
    system_prompt TEXT NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'disabled',
    max_concurrency INT NOT NULL DEFAULT 2,
    history_max_messages INT NOT NULL DEFAULT 40,
    pairing_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT im_bots_platform_check CHECK (platform IN ('telegram', 'feishu', 'dingtalk', 'wecom', 'qq', 'slack', 'wechat', 'whatsapp')),
    CONSTRAINT im_bots_status_check CHECK (status IN ('enabled', 'disabled', 'error'))
);

CREATE UNIQUE INDEX IF NOT EXISTS im_bots_name_active_uidx ON im_bots (name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS im_bots_status_idx ON im_bots (status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS im_bots_api_key_idx ON im_bots (api_key_id);

-- Paired chat windows: one row per (bot, platform chat id), created on successful pairing.
CREATE TABLE IF NOT EXISTS im_bot_chats (
    id BIGSERIAL PRIMARY KEY,
    bot_id BIGINT NOT NULL,
    chat_id VARCHAR(255) NOT NULL,
    platform_user_id VARCHAR(255) NOT NULL DEFAULT '',
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    session_uuid VARCHAR(36) NOT NULL,
    model_override VARCHAR(200) NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    paired_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_message_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT im_bot_chats_status_check CHECK (status IN ('active', 'blocked')),
    CONSTRAINT im_bot_chats_bot_fk FOREIGN KEY (bot_id) REFERENCES im_bots (id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS im_bot_chats_bot_chat_uidx ON im_bot_chats (bot_id, chat_id);
CREATE INDEX IF NOT EXISTS im_bot_chats_last_msg_idx ON im_bot_chats (bot_id, last_message_at DESC);

-- Chat history for multi-turn context rebuild.
CREATE TABLE IF NOT EXISTS im_bot_messages (
    id BIGSERIAL PRIMARY KEY,
    chat_id BIGINT NOT NULL,
    bot_id BIGINT NOT NULL,
    role VARCHAR(16) NOT NULL,
    content TEXT NOT NULL,
    request_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT im_bot_messages_role_check CHECK (role IN ('user', 'assistant')),
    CONSTRAINT im_bot_messages_chat_fk FOREIGN KEY (chat_id) REFERENCES im_bot_chats (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS im_bot_messages_chat_time_idx ON im_bot_messages (chat_id, created_at DESC);
CREATE INDEX IF NOT EXISTS im_bot_messages_bot_time_idx ON im_bot_messages (bot_id, created_at DESC);
