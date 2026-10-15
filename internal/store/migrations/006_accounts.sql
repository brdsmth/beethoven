-- People who sign in to the web UI. The first account owns the server.
CREATE TABLE accounts (
  id            TEXT PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  owner         INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL
);

-- Signed-in browsers, by the SHA-256 of their cookie: the cookie itself is
-- never stored.
CREATE TABLE logins (
  id         TEXT PRIMARY KEY,
  account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE INDEX logins_account ON logins(account_id);
