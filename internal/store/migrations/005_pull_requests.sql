-- Pull requests sessions opened, with their state on GitHub, which a poller
-- refreshes until they are merged or closed.
CREATE TABLE pull_requests (
  repo       TEXT NOT NULL,                 -- owner/name
  number     INTEGER NOT NULL,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  url        TEXT NOT NULL UNIQUE,
  state      TEXT NOT NULL DEFAULT 'open',  -- open, merged or closed (without merging)
  draft      INTEGER NOT NULL DEFAULT 0,
  opened_at  INTEGER NOT NULL DEFAULT 0,    -- when GitHub created it; 0 until first synced
  merged_at  INTEGER,
  closed_at  INTEGER,
  etag       TEXT NOT NULL DEFAULT '',      -- for conditional requests
  synced_at  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (repo, number)
);
CREATE INDEX pull_requests_session ON pull_requests(session_id);
CREATE INDEX pull_requests_open ON pull_requests(state, synced_at);
