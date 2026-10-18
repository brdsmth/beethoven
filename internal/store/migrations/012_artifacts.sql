-- Files shown in a session's viewer: a snapshot of a file in the sandbox,
-- kept after the sandbox stops. Showing the same path again adds a version.
CREATE TABLE artifacts (
  id           TEXT PRIMARY KEY,
  session_id   TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  prompt_id    TEXT NOT NULL DEFAULT '',
  path         TEXT NOT NULL,
  name         TEXT NOT NULL,
  title        TEXT NOT NULL DEFAULT '',
  content_type TEXT NOT NULL,
  size         INTEGER NOT NULL,
  data         BLOB NOT NULL,
  created_at   INTEGER NOT NULL
);
CREATE INDEX artifacts_by_session ON artifacts (session_id, created_at);
