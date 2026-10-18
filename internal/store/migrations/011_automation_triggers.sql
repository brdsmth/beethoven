-- An automation can fire on events from a git host: issue labeled, pull
-- request opened… The trigger is JSON: {"event": kind, "conditions": [...]}.
ALTER TABLE automations ADD COLUMN trigger_event TEXT NOT NULL DEFAULT '';
ALTER TABLE automations ADD COLUMN trigger_conditions TEXT NOT NULL DEFAULT '[]';
CREATE INDEX automations_by_trigger ON automations (trigger_event) WHERE trigger_event != '';
