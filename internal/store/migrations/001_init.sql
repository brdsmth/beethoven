CREATE TABLE IF NOT EXISTS sessions (
  id            TEXT PRIMARY KEY,
  title         TEXT NOT NULL,
  repo          TEXT NOT NULL DEFAULT '',      -- owner/name; '' = scratch workspace
  base_branch   TEXT NOT NULL DEFAULT '',      -- '' = repository default
  branch        TEXT NOT NULL,                 -- work branch the agent commits to
  model         TEXT NOT NULL DEFAULT '',
  parent_id     TEXT REFERENCES sessions(id) ON DELETE SET NULL,
  depth         INTEGER NOT NULL DEFAULT 0,
  source        TEXT NOT NULL,
  key           TEXT UNIQUE,                   -- continuation key: triggers with this key reuse the session
  automation_id TEXT,
  author_name   TEXT NOT NULL DEFAULT '',
  author_email  TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL DEFAULT 'idle',  -- idle|running|done|failed|stopped
  sandbox       TEXT NOT NULL DEFAULT 'none',  -- none|starting|ready|stopped|failed
  sandbox_id    TEXT NOT NULL DEFAULT '',
  agent_session TEXT NOT NULL DEFAULT '',      -- harness conversation id, for resume
  pr_url        TEXT NOT NULL DEFAULT '',
  cost_usd      REAL NOT NULL DEFAULT 0,
  archived      INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_parent ON sessions(parent_id);
CREATE INDEX IF NOT EXISTS sessions_updated ON sessions(updated_at DESC);

-- The prompt queue. One row per prompt; FIFO per session by (created_at, rowid).
CREATE TABLE IF NOT EXISTS prompts (
  id            TEXT PRIMARY KEY,
  session_id    TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  content       TEXT NOT NULL,
  model         TEXT NOT NULL DEFAULT '',
  author_name   TEXT NOT NULL DEFAULT '',
  author_email  TEXT NOT NULL DEFAULT '',
  source        TEXT NOT NULL,
  automation_id TEXT,
  dedup_key     TEXT UNIQUE,
  status        TEXT NOT NULL,                 -- queued|running|done|failed|cancelled
  error         TEXT NOT NULL DEFAULT '',
  result        TEXT NOT NULL DEFAULT '',      -- the agent's final response
  cost_usd      REAL NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL,
  started_at    INTEGER,
  finished_at   INTEGER
);
CREATE INDEX IF NOT EXISTS prompts_queue ON prompts(session_id, status, created_at);
CREATE INDEX IF NOT EXISTS prompts_automation ON prompts(automation_id, created_at DESC);

-- The session timeline: an append-only log with a dense per-session sequence.
CREATE TABLE IF NOT EXISTS events (
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  seq        INTEGER NOT NULL,
  type       TEXT NOT NULL,
  prompt_id  TEXT NOT NULL DEFAULT '',
  data       TEXT NOT NULL,
  at         INTEGER NOT NULL,
  PRIMARY KEY (session_id, seq)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS automations (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  repo        TEXT NOT NULL DEFAULT '',
  base_branch TEXT NOT NULL DEFAULT '',
  prompt      TEXT NOT NULL,
  model       TEXT NOT NULL DEFAULT '',
  schedule    TEXT NOT NULL DEFAULT '',        -- cron expression; '' = webhook/manual only
  reuse       INTEGER NOT NULL DEFAULT 0,      -- 1 = every run continues one session
  enabled     INTEGER NOT NULL DEFAULT 1,
  failures    INTEGER NOT NULL DEFAULT 0,      -- consecutive failed runs; 3 pauses the automation
  next_run_at INTEGER,
  hook_hash   TEXT NOT NULL,                   -- sha256 of the webhook token
  created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS secrets (
  scope      TEXT NOT NULL,                    -- '' = global, else owner/name
  name       TEXT NOT NULL,
  value      BLOB NOT NULL,                    -- AES-256-GCM: nonce || ciphertext
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (scope, name)
);
