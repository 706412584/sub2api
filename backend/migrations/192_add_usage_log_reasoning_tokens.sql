-- Store upstream reasoning/thinking token counts for usage display.
-- OpenAI/Grok: completion_tokens_details.reasoning_tokens / output_tokens_details.reasoning_tokens.
-- Historical rows remain 0 (unknown). Does not change billing (output already includes reasoning where applicable).
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS reasoning_tokens INTEGER NOT NULL DEFAULT 0;
