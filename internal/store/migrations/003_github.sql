-- GitHub users who authorized the server's GitHub App. Their tokens were
-- issued by that app, so they are kept per app; the server acts for the
-- most recently connected account of its current app.
CREATE TABLE github_accounts (
  app_id       INTEGER NOT NULL,
  id           INTEGER NOT NULL, -- GitHub user id
  login        TEXT NOT NULL,
  name         TEXT NOT NULL DEFAULT '',
  tokens       BLOB NOT NULL,    -- encrypted JSON: access and refresh tokens with their expiry
  connected_at INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  PRIMARY KEY (app_id, id)
);
