-- Work is private until it's shared. Everyone gets a personal team, seen by
-- them alone, where their work goes unless they choose another team (an
-- empty default_team_id now means theirs); a session can also be shared
-- with named people. Short names starting with "personal-" are reserved
-- for these teams: a team already using one keeps it, but can't take one.
INSERT OR IGNORE INTO teams (id, slug, name, description, join_policy, default_visibility, created_at, updated_at)
SELECT 'personal-' || id, 'personal-' || id, 'Personal', 'Only you see what''s here.', 'invite', 'team',
  created_at, created_at FROM accounts;
INSERT OR IGNORE INTO team_members (team_id, account_id, role, created_at)
SELECT 'personal-' || id, id, 'lead', created_at FROM accounts;

-- People a session is shared with, beyond who its visibility lets in. They
-- may read it. (Accounts are deactivated, never deleted.)
CREATE TABLE session_shares (
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  account_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (session_id, account_id)
);
CREATE INDEX session_shares_account ON session_shares(account_id, session_id);
