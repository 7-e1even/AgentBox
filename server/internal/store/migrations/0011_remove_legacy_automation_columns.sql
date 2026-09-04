-- The create-sandbox-only Automation model no longer reads these legacy
-- run-task result and cleanup columns. Drop them so historical guest output
-- cannot remain as an ungoverned data sink after migration 0010.
ALTER TABLE automation_runs
  DROP COLUMN IF EXISTS exit_code,
  DROP COLUMN IF EXISTS output,
  DROP COLUMN IF EXISTS output_truncated,
  DROP COLUMN IF EXISTS cleanup_status,
  DROP COLUMN IF EXISTS expires_at;
