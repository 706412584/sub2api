-- proxies: optional upstream/egress proxy for proxy chaining
ALTER TABLE proxies ADD COLUMN IF NOT EXISTS egress_proxy_id BIGINT REFERENCES proxies(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS proxies_egress_proxy_id_idx ON proxies (egress_proxy_id);
