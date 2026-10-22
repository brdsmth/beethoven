-- Threads elsewhere that sessions take part in: a pull request a session
-- opened, an issue or pull request it commented on or was started for, a
-- branch it pushed. What happens in a thread comes back to its sessions.
-- subject names the thread host-neutrally: "github:owner/name:pr:42".
CREATE TABLE session_links (
  subject    TEXT NOT NULL,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  role       TEXT NOT NULL,             -- owner (it opened the thread) or follower
  reason     TEXT NOT NULL DEFAULT '',  -- opened, commented, triggered, asked
  muted      INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  closed_at  INTEGER,                   -- the thread ended: merged or closed
  PRIMARY KEY (subject, session_id)
);
CREATE INDEX session_links_session ON session_links(session_id);

-- What sessions posted elsewhere, so their own words coming back are known:
-- not sent back to them, and counted as a hop between agents.
CREATE TABLE outbound (
  ref        TEXT PRIMARY KEY,          -- the comment's URL
  session_id TEXT NOT NULL,
  prompt_id  TEXT NOT NULL DEFAULT '',
  hop        INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

-- What happened in a session's threads, waiting to be handed to it as a
-- prompt. Items arriving together are delivered together.
CREATE TABLE inbox (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id   TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  subject      TEXT NOT NULL,
  occurrence   TEXT NOT NULL,           -- the host's id for the happening: each is delivered once
  kind         TEXT NOT NULL,
  actor        TEXT NOT NULL DEFAULT '',
  data         TEXT NOT NULL,           -- the event, JSON
  received_at  INTEGER NOT NULL,
  delivered_at INTEGER,
  prompt_id    TEXT NOT NULL DEFAULT '',
  outcome      TEXT NOT NULL DEFAULT '', -- delivered, or why not
  UNIQUE (session_id, occurrence)
);
CREATE INDEX inbox_pending ON inbox(delivered_at, received_at);

-- How many agent-to-agent exchanges led to a prompt: 0 for people, one
-- more each time an agent's words set another agent off.
ALTER TABLE prompts ADD COLUMN hop INTEGER NOT NULL DEFAULT 0;
