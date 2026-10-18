-- The hosted service's own records: its workspaces, and how traffic from
-- GitHub and Slack finds them. Each workspace's data is in a database of
-- its own, under workspaces/<id>/.
CREATE TABLE workspaces (
  id              TEXT PRIMARY KEY,
  slug            TEXT NOT NULL UNIQUE,          -- its subdomain
  name            TEXT NOT NULL,
  status          TEXT NOT NULL DEFAULT 'active', -- active or suspended
  plan            TEXT NOT NULL DEFAULT 'trial',
  data_key        BLOB NOT NULL,                  -- its encryption key, sealed with the master key
  claim_hash      TEXT NOT NULL DEFAULT '',       -- SHA-256 of the code that claims it, until claimed
  claim_expires   INTEGER,
  migrations      INTEGER NOT NULL DEFAULT 0,     -- how many its database has applied
  last_migration  TEXT NOT NULL DEFAULT '',
  migrated_at     INTEGER,                        -- when it last applied one
  migration_error TEXT NOT NULL DEFAULT '',       -- why it last failed to open, if it did
  created_at      INTEGER NOT NULL
);

-- Which workspace a Slack workspace (team id) belongs to: Slack sends
-- every workspace's events to one address.
CREATE TABLE routes (
  kind         TEXT NOT NULL,
  external_id  TEXT NOT NULL,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  created_at   INTEGER NOT NULL,
  PRIMARY KEY (kind, external_id)
);
