-- Add Grok reasoning quality check fields for dynamic proxy pools.
-- When enabled, the pool's proxies are periodically probed through a selected
-- Grok OAuth account to detect visible reasoning, and the entry proxy only
-- selects proxies that pass (visible reasoning) once probed.
ALTER TABLE dynamic_proxy_pools
    ADD COLUMN IF NOT EXISTS grok_reasoning_check_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS grok_reasoning_check_account_id BIGINT,
    ADD COLUMN IF NOT EXISTS grok_reasoning_check_interval_sec INT NOT NULL DEFAULT 300;