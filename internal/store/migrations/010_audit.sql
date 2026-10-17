-- Changes to who may do what: people, teams, memberships, secrets, channel
-- bindings and session visibility. Written in the transaction that makes
-- the change. Secret values are never recorded, only names.
CREATE TABLE audit_events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  at         INTEGER NOT NULL,
  actor_id   TEXT NOT NULL DEFAULT '',   -- the account; '' = the server
  actor      TEXT NOT NULL DEFAULT '',   -- its name at the time
  team_id    TEXT NOT NULL DEFAULT '',
  action     TEXT NOT NULL,              -- e.g. team.member_added
  target     TEXT NOT NULL DEFAULT '',   -- what it acted on: an account, a session, a secret name
  detail     TEXT NOT NULL DEFAULT '{}'  -- JSON: what changed
);
CREATE INDEX audit_events_at ON audit_events(at DESC);
CREATE INDEX audit_events_team ON audit_events(team_id, at DESC);
