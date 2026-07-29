-- Configure the upstream protocol used for native Anthropic Messages requests on Grok groups.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS grok_messages_protocol VARCHAR(32) NOT NULL DEFAULT 'chat_completions';

UPDATE groups
SET grok_messages_protocol = CASE
    WHEN platform = 'grok' THEN 'chat_completions'
    ELSE 'responses'
END
WHERE grok_messages_protocol IS NULL
   OR grok_messages_protocol NOT IN ('chat_completions', 'responses')
   OR platform <> 'grok';

ALTER TABLE groups
    DROP CONSTRAINT IF EXISTS groups_grok_messages_protocol_check;

ALTER TABLE groups
    ADD CONSTRAINT groups_grok_messages_protocol_check
    CHECK (grok_messages_protocol IN ('chat_completions', 'responses'));
