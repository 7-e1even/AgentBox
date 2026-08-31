CREATE TABLE audit_outbox (
  id UUID PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL,
  entry JSONB NOT NULL,
  delivered_at TIMESTAMPTZ
);

CREATE INDEX idx_audit_outbox_pending
  ON audit_outbox(created_at, id) WHERE delivered_at IS NULL;

ALTER TABLE system_logs ADD COLUMN audit_event_id UUID;
CREATE UNIQUE INDEX idx_system_logs_audit_event ON system_logs(audit_event_id);
