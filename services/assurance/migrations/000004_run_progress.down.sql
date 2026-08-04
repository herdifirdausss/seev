ALTER TABLE intake_control_commands DROP COLUMN IF EXISTS resulting_revision;
ALTER TABLE intake_control_commands DROP COLUMN IF EXISTS reason;
ALTER TABLE assurance_runs DROP COLUMN IF EXISTS pages_scanned;
ALTER TABLE assurance_runs DROP COLUMN IF EXISTS cutoff_at;
