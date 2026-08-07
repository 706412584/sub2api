-- Per-group Grok visible-reasoning scheduling mode.
-- ''       = inherit the gateway-level default (system settings)
-- off      = ignore reasoning quality marks entirely
-- soft     = keep scheduling but deprioritize non-visible accounts (current behavior)
-- enforce  = exclude non-visible accounts and quarantine them until the mark expires
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS grok_reasoning_visibility_mode VARCHAR(16) NOT NULL DEFAULT '';

UPDATE groups
SET grok_reasoning_visibility_mode = ''
WHERE grok_reasoning_visibility_mode IS NULL
   OR grok_reasoning_visibility_mode NOT IN ('', 'off', 'soft', 'enforce');

ALTER TABLE groups
    DROP CONSTRAINT IF EXISTS groups_grok_reasoning_visibility_mode_check;

ALTER TABLE groups
    ADD CONSTRAINT groups_grok_reasoning_visibility_mode_check
    CHECK (grok_reasoning_visibility_mode IN ('', 'off', 'soft', 'enforce'));
