ALTER TABLE intake_control_commands DROP CONSTRAINT IF EXISTS intake_control_commands_status_check;
ALTER TABLE intake_control_commands ADD CONSTRAINT intake_control_commands_status_check CHECK (status IN ('pending','applied','rejected','failed'));
