ALTER TABLE managed_servers
  ADD COLUMN IF NOT EXISTS last_job_activity_at TIMESTAMPTZ;

UPDATE managed_servers
SET last_job_activity_at = last_seen_at
WHERE last_job_activity_at IS NULL;

ALTER TABLE managed_servers
  ALTER COLUMN last_job_activity_at SET DEFAULT NOW(),
  ALTER COLUMN last_job_activity_at SET NOT NULL;
