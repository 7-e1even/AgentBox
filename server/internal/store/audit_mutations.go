package store

import (
	"context"

	"agentbox/internal/platform"
	"github.com/jackc/pgx/v5"
)

// A critical mutation succeeds only when its durable audit record can commit.
func (s *Store) commitAudit(ctx context.Context, tx pgx.Tx, entry platform.LogEntry) error {
	if err := s.appendAuditEvent(ctx, tx, entry); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
