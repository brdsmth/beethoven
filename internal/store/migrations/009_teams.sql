-- Teams group people and own work. Ownership is optional: '' means the
-- workspace owns a row, as everything was before teams, so nothing needs
-- backfilling.
CREATE TABLE teams (
  id                 TEXT PRIMARY KEY,
  slug               TEXT NOT NULL UNIQUE COLLATE NOCASE,
  name               TEXT NOT NULL,
  description        TEXT NOT NULL DEFAULT '',
  color              TEXT NOT NULL DEFAULT '',        -- a #rrggbb accent for the UI
  join_policy        TEXT NOT NULL DEFAULT 'invite',  -- open: anyone may join; invite: leads add people
  default_visibility TEXT NOT NULL DEFAULT 'team',    -- for new sessions: team or workspace
  default_environment_id TEXT NOT NULL DEFAULT '',    -- the project new sessions start in
  archived_at        INTEGER,                         -- archived teams take no new work; never deleted
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL
);

CREATE TABLE team_members (
  team_id    TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  role       TEXT NOT NULL DEFAULT 'member',          -- lead or member
  created_at INTEGER NOT NULL,
  PRIMARY KEY (team_id, account_id)
);
CREATE INDEX team_members_account ON team_members(account_id, team_id);

-- Where a person's work goes when nothing else says: Slack DMs, the UI.
ALTER TABLE accounts ADD COLUMN default_team_id TEXT NOT NULL DEFAULT '';

-- Who owns a session, and who sees it: everyone (workspace), the owning
-- team's members (team), or only the person who started it (private).
ALTER TABLE sessions ADD COLUMN owner_team_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN visibility TEXT NOT NULL DEFAULT 'workspace';
CREATE INDEX sessions_team ON sessions(owner_team_id, updated_at DESC);

ALTER TABLE automations ADD COLUMN owner_team_id TEXT NOT NULL DEFAULT '';

-- The team a Slack channel's requests belong to. (Slack's own team_id in
-- these tables is the Slack workspace.)
ALTER TABLE slack_channels ADD COLUMN owner_team_id TEXT NOT NULL DEFAULT '';

-- Projects (environments) belong to a team or the workspace; names are
-- unique per owner. SQLite can't change a constraint in place: rebuild.
CREATE TABLE environments_new (
  id              TEXT PRIMARY KEY,
  owner_team_id   TEXT NOT NULL DEFAULT '',
  name            TEXT NOT NULL COLLATE NOCASE,
  description     TEXT NOT NULL DEFAULT '',
  repos           TEXT NOT NULL DEFAULT '[]',
  setup_script    TEXT NOT NULL DEFAULT '',
  start_script    TEXT NOT NULL DEFAULT '',
  inherit_secrets INTEGER NOT NULL DEFAULT 1,
  overrides       TEXT NOT NULL DEFAULT '{}',
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL,
  UNIQUE (owner_team_id, name)
);
INSERT INTO environments_new (id, name, description, repos, setup_script, start_script, inherit_secrets, overrides,
  created_at, updated_at)
SELECT id, name, description, repos, setup_script, start_script, inherit_secrets, overrides, created_at, updated_at
FROM environments;
DROP TABLE environments;
ALTER TABLE environments_new RENAME TO environments;
