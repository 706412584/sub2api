-- Fix CHECK constraint to accept 'inherit' as a valid value.
-- The schema defaults to '' (inherit), but the explicit Normalize function
-- returns 'inherit', which the DB constraint previously rejected.
ALTER TABLE groups
    DROP CONSTRAINT IF EXISTS groups_grok_reasoning_visibility_mode_check;

ALTER TABLE groups
    ADD CONSTRAINT groups_grok_reasoning_visibility_mode_check
    CHECK (grok_reasoning_visibility_mode IN ('', 'inherit', 'off', 'soft', 'enforce'));