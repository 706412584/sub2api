-- Per-group Grok reasoning probe reuse TTL.
-- -1 = inherit from gateway setting (default)
--  0 = always re-probe for every selection
-- >0 = cache probe result for N seconds
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS grok_reasoning_probe_ttl_sec INTEGER NOT NULL DEFAULT -1;