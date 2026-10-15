-- Files attached to prompts: uploaded in the web UI or shared in Slack.
-- The content is the last column, so reading the others never loads it.
CREATE TABLE attachments (
  id           TEXT PRIMARY KEY,
  prompt_id    TEXT NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
  session_id   TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,                  -- a file name, unique within the prompt
  content_type TEXT NOT NULL,
  size         INTEGER NOT NULL,
  data         BLOB NOT NULL
);
CREATE INDEX attachments_prompt ON attachments(prompt_id);
CREATE INDEX attachments_session ON attachments(session_id);
