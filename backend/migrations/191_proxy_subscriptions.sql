-- Embedded proxy subscription sources (Clash / share-link) managed by Sub2API.
CREATE TABLE IF NOT EXISTS proxy_subscriptions (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    name VARCHAR(100) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    source_type VARCHAR(20) NOT NULL DEFAULT 'url',
    subscription_url VARCHAR(2000) NOT NULL DEFAULT '',
    inline_body TEXT NOT NULL DEFAULT '',
    name_prefix VARCHAR(40) NOT NULL DEFAULT 'sidecar-a-',
    protocol VARCHAR(20) NOT NULL DEFAULT 'socks5',
    bind_address VARCHAR(64) NOT NULL DEFAULT '127.0.0.1',
    base_port INTEGER NOT NULL DEFAULT 21080,
    max_ports INTEGER NOT NULL DEFAULT 10,
    sync_interval_sec INTEGER NOT NULL DEFAULT 300,
    node_allow_contains JSONB NOT NULL DEFAULT '[]'::jsonb,
    last_sync_at TIMESTAMPTZ NULL,
    last_sync_status VARCHAR(40) NOT NULL DEFAULT '',
    last_sync_error TEXT NOT NULL DEFAULT '',
    last_config_hash VARCHAR(64) NOT NULL DEFAULT '',
    desired_count INTEGER NOT NULL DEFAULT 0,
    created_by BIGINT NOT NULL DEFAULT 0,
    next_due_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_proxy_subscriptions_enabled_due
    ON proxy_subscriptions (enabled, next_due_at);

CREATE INDEX IF NOT EXISTS idx_proxy_subscriptions_name_prefix
    ON proxy_subscriptions (name_prefix);

CREATE INDEX IF NOT EXISTS idx_proxy_subscriptions_enabled
    ON proxy_subscriptions (enabled);
