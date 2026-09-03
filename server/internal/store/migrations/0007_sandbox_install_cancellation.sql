-- A requested cancellation keeps its lease until the Worker confirms cleanup.
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS cancel_requested_at TIMESTAMPTZ;
