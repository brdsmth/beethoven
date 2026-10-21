-- What each prompt's turn used, as the agent reported it: tokens, turns,
-- and the model that did most of the work. Earlier prompts report zero.
ALTER TABLE prompts ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE prompts ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE prompts ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE prompts ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE prompts ADD COLUMN turns INTEGER NOT NULL DEFAULT 0;
ALTER TABLE prompts ADD COLUMN agent_model TEXT NOT NULL DEFAULT '';

-- Insights read a window's events of a few types (tool calls, sandbox
-- states) without walking every message.
CREATE INDEX events_type ON events(type, session_id);
