-- Slack workspaces the server's Slack app is installed in.
CREATE TABLE slack_installations (
  team_id     TEXT PRIMARY KEY,
  app_id      TEXT NOT NULL,             -- the app that issued the token
  team_name   TEXT NOT NULL DEFAULT '',
  bot_user_id TEXT NOT NULL,
  bot_token   BLOB NOT NULL,             -- encrypted
  installer   TEXT NOT NULL DEFAULT '',  -- Slack user id
  base_url    TEXT NOT NULL DEFAULT '',  -- the server's URL, for links from Slack
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

-- Requests made in Slack, by the dedup key of the prompt each became, to
-- answer in their thread. A request is recorded before its prompt is
-- queued, so a redelivered event finds it.
CREATE TABLE slack_requests (
  key        TEXT PRIMARY KEY,
  team_id    TEXT NOT NULL,
  channel    TEXT NOT NULL,
  thread_ts  TEXT NOT NULL,
  message_ts TEXT NOT NULL,           -- the request
  status_ts  TEXT NOT NULL DEFAULT '', -- the bot's status reply
  created_at INTEGER NOT NULL
);
CREATE INDEX slack_requests_thread ON slack_requests(team_id, channel, thread_ts);

-- What a channel works on when a request names nothing.
CREATE TABLE slack_channels (
  team_id        TEXT NOT NULL,
  channel        TEXT NOT NULL,
  name           TEXT NOT NULL DEFAULT '',
  environment_id TEXT NOT NULL DEFAULT '',
  repos          TEXT NOT NULL DEFAULT '[]', -- with no environment; [] for a scratch workspace
  updated_at     INTEGER NOT NULL,
  PRIMARY KEY (team_id, channel)
);
