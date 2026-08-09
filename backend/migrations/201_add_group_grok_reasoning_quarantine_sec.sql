-- Per-group Grok reasoning enforce quarantine cooldown.
-- -1 = inherit from gateway setting (default)
-- -2 = pause account scheduling (SetSchedulable false)
--  0 = exclude this selection only (no temp-unschedulable write)
-- >0 = SetTempUnschedulable for N seconds
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS grok_reasoning_quarantine_sec INTEGER NOT NULL DEFAULT -1;
