-- The partial retention indexes are built and verified online after the
-- transactional migrations commit. Keeping this marker in the checksum ledger
-- makes the schema transition explicit without blocking system_logs writers.
SELECT 1;
