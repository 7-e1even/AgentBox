ALTER TABLE control_resources
  ADD COLUMN spec_version INTEGER NOT NULL DEFAULT 1 CHECK (spec_version > 0),
  ADD COLUMN generation BIGINT NOT NULL DEFAULT 1 CHECK (generation > 0),
  ADD COLUMN observed_generation BIGINT NOT NULL DEFAULT 1
    CHECK (observed_generation >= 0 AND observed_generation <= generation);

UPDATE control_resources SET observed_generation = 0
WHERE kind = 'sandbox' AND COALESCE(spec->>'status', '') NOT IN ('running', 'stopped');

ALTER TABLE worker_jobs ADD COLUMN resource_generation BIGINT NOT NULL DEFAULT 1
  CHECK (resource_generation > 0);
