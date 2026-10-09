-- groups: 默认出站代理（账号加入分组且未显式指定代理时自动绑定）
ALTER TABLE groups ADD COLUMN IF NOT EXISTS default_proxy_id BIGINT REFERENCES proxies(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_groups_default_proxy_id ON groups(default_proxy_id) WHERE deleted_at IS NULL AND default_proxy_id IS NOT NULL;
