-- The capabilities each repository named or looked like it needed when a
-- sandbox last booted with it, so the next sandbox is sized for them and
-- given their cached installs up front.
CREATE TABLE repo_capabilities (
  repo       TEXT PRIMARY KEY,
  ids        TEXT NOT NULL, -- JSON list
  updated_at INTEGER NOT NULL
);
