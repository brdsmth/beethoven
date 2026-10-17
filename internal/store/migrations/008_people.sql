-- Several people per server. The owner flag becomes a role: owner, admin
-- or member. A deactivated account keeps its history but can't sign in.
ALTER TABLE accounts ADD COLUMN role TEXT NOT NULL DEFAULT 'member';
UPDATE accounts SET role = 'owner' WHERE owner = 1;
ALTER TABLE accounts ADD COLUMN disabled_at INTEGER;

-- Invitations: single-use links, stored by the SHA-256 of their token.
CREATE TABLE invites (
  id         TEXT PRIMARY KEY,
  role       TEXT NOT NULL,                 -- the role the new account gets
  email      TEXT NOT NULL DEFAULT '',      -- whom it's for; '' = anyone with the link
  team_id    TEXT NOT NULL DEFAULT '',      -- a team the new account joins
  created_by TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  used_by    TEXT NOT NULL DEFAULT '',
  used_at    INTEGER
);

-- Personal API tokens, for scripts and the CLI, by the SHA-256 of the token.
CREATE TABLE api_tokens (
  id           TEXT PRIMARY KEY,
  account_id   TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  hint         TEXT NOT NULL,               -- the token's last characters, to tell tokens apart
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER
);
CREATE INDEX api_tokens_account ON api_tokens(account_id);

-- The person a session, prompt or automation acts for: their GitHub opens
-- the pull requests. '' = nobody in particular (the server's default).
ALTER TABLE sessions ADD COLUMN account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE prompts ADD COLUMN account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE automations ADD COLUMN account_id TEXT NOT NULL DEFAULT '';

-- Each person connects their own GitHub. Connections made before people
-- existed belong to the owner.
ALTER TABLE github_accounts ADD COLUMN account_id TEXT NOT NULL DEFAULT '';
UPDATE github_accounts SET account_id = COALESCE((SELECT id FROM accounts WHERE role = 'owner' LIMIT 1), '');
CREATE INDEX github_accounts_person ON github_accounts(app_id, account_id);
