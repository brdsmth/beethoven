-- Work is private until it's shared. Everyone gets a personal team, seen by
-- them alone, where their work goes unless they choose another team; a
-- session can also be shared with named people.
INSERT OR IGNORE INTO teams (id, slug, name, description, join_policy, default_visibility, created_at, updated_at)
SELECT 'personal-' || id, 'personal-' || id, 'Personal', 'Only you see what''s here.', 'invite', 'team',
  created_at, created_at FROM accounts;
INSERT OR IGNORE INTO team_members (team_id, account_id, role, created_at)
SELECT 'personal-' || id, id, 'lead', created_at FROM accounts;

-- Whoever has no default team, or one they've since left, now works in
-- their personal team.
UPDATE accounts SET default_team_id = 'personal-' || id
WHERE default_team_id = '' OR NOT EXISTS (SELECT 1 FROM team_members m
  WHERE m.team_id = accounts.default_team_id AND m.account_id = accounts.id);

-- People a session is shared with, beyond who its visibility lets in. They
-- may read it. (Accounts are deactivated, never deleted.)
CREATE TABLE session_shares (
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  account_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (session_id, account_id)
);
CREATE INDEX session_shares_account ON session_shares(account_id, session_id);
