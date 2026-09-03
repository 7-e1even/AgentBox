package store

import (
	"context"
	"fmt"

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

// RecordDurableAudit records a security-relevant event without a companion
// business mutation. The outbox insert itself is the transaction boundary.
func (s *Store) RecordDurableAudit(ctx context.Context, entry platform.LogEntry) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin durable audit: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := s.appendAuditEvent(ctx, tx, entry); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit durable audit: %w", err)
	}
	return nil
}
