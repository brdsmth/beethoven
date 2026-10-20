-- What an invitation tells its person: a note from whoever sent it, and a
-- first session to try, in a project.
ALTER TABLE invites ADD COLUMN note TEXT NOT NULL DEFAULT '';
ALTER TABLE invites ADD COLUMN first_prompt TEXT NOT NULL DEFAULT '';
ALTER TABLE invites ADD COLUMN environment_id TEXT NOT NULL DEFAULT '';

-- Getting started, while someone is: JSON (see store.Onboarding); '' once
-- they're done or hid it. People who were here before it began are done.
ALTER TABLE accounts ADD COLUMN onboarding TEXT NOT NULL DEFAULT '';
