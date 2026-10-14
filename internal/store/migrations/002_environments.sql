-- Sessions and automations target an ordered list of repositories (the
-- first is the primary), optionally taken from an environment.
ALTER TABLE sessions ADD COLUMN repos TEXT NOT NULL DEFAULT '[]';
UPDATE sessions SET repos = json_array(json_object('repo', repo, 'base_branch', base_branch, 'pr_url', pr_url))
  WHERE repo != '';
ALTER TABLE sessions DROP COLUMN repo;
ALTER TABLE sessions DROP COLUMN base_branch;
ALTER TABLE sessions DROP COLUMN pr_url;
ALTER TABLE sessions ADD COLUMN environment_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN effort TEXT NOT NULL DEFAULT '';
ALTER TABLE prompts ADD COLUMN effort TEXT NOT NULL DEFAULT '';

ALTER TABLE automations ADD COLUMN repos TEXT NOT NULL DEFAULT '[]';
UPDATE automations SET repos = json_array(json_object('repo', repo, 'base_branch', base_branch))
  WHERE repo != '';
ALTER TABLE automations DROP COLUMN repo;
ALTER TABLE automations DROP COLUMN base_branch;
ALTER TABLE automations ADD COLUMN environment_id TEXT NOT NULL DEFAULT '';
ALTER TABLE automations ADD COLUMN effort TEXT NOT NULL DEFAULT '';

-- A reusable workspace definition. Sessions copy its repositories when they
-- start; its secrets, scripts and overrides are read live.
CREATE TABLE environments (
  id              TEXT PRIMARY KEY,
  name            TEXT NOT NULL UNIQUE COLLATE NOCASE,
  description     TEXT NOT NULL DEFAULT '',
  repos           TEXT NOT NULL DEFAULT '[]',
  setup_script    TEXT NOT NULL DEFAULT '',   -- runs once per sandbox, after cloning
  start_script    TEXT NOT NULL DEFAULT '',   -- runs on every sandbox start
  inherit_secrets INTEGER NOT NULL DEFAULT 1, -- also inject global secrets
  overrides       TEXT NOT NULL DEFAULT '{}', -- settings that override the global ones
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);

-- Global settings, one JSON document.
CREATE TABLE settings (
  id    INTEGER PRIMARY KEY CHECK (id = 1),
  value TEXT NOT NULL
);

-- Extra MCP servers offered to agents, by scope.
CREATE TABLE mcp_servers (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  scope      TEXT NOT NULL DEFAULT '',   -- '' = global, 'env:<id>', or owner/name
  type       TEXT NOT NULL,              -- http | stdio
  url        TEXT NOT NULL DEFAULT '',
  command    TEXT NOT NULL DEFAULT '[]', -- argv, for stdio
  secret     BLOB,                       -- encrypted JSON object: headers (http) or env (stdio)
  enabled    INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
