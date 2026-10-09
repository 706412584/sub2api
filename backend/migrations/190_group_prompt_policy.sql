-- Add a per-group prompt processing policy for text request interception.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS prompt_policy JSONB NOT NULL DEFAULT '{}'::jsonb;
