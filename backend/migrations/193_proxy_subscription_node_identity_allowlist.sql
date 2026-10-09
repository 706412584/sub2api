-- Exact node identity allowlist for embedded proxy subscriptions.
-- Empty array keeps auto-select (max_ports + optional name filter) behavior.
ALTER TABLE proxy_subscriptions
    ADD COLUMN IF NOT EXISTS node_identity_allowlist JSONB NOT NULL DEFAULT '[]'::jsonb;
