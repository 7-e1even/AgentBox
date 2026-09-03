package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentbox/internal/platform"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// appendAuditEvent must share the caller's business transaction. Only fixed
// metadata fields are copied: no spec, request/response body, error text, or
// credential values belong in the durable audit trail.
func (s *Store) appendAuditEvent(ctx context.Context, tx pgx.Tx, entry platform.LogEntry) error {
	actor := platform.AuditActorFromContext(ctx)
	if actor.Type == "" {
		actor.Type = "system"
	}
	if entry.ActorID == "" {
		entry.ActorID, entry.ActorName = actor.ID, actor.Name
	} else if entry.ActorName == "" && entry.ActorID == actor.ID {
		entry.ActorName = actor.Name
	}
	if entry.Level == "" {
		entry.Level = platform.LogLevelInfo
	}
	if entry.Status == "" {
		entry.Status = platform.LogStatusSuccess
	}
	entry.ID = 0
	entry.CreatedAt = time.Now().UTC()
	entry.Message = entry.Category + "." + entry.Action
	entry.RemoteAddr = ""
	detail := auditMetadata(entry.Detail)
	id := uuid.NewString()
	detail["auditEventId"] = id
	detail["actorType"] = actor.Type
	detail["delivery"] = "transactional"
	entry.Detail = detail
	encoded, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode audit event: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_outbox (id, created_at, entry)
	  VALUES ($1, $2, $3::jsonb)`, id, entry.CreatedAt, encoded); err != nil {
		return fmt.Errorf("append audit event: %w", err)
	}
	return nil
}

func auditMetadata(input map[string]any) map[string]any {
	out := make(map[string]any)
	for _, key := range []string{
		"projectId", "templateId", "serverId", "jobId", "runId", "driver", "action",
		"role", "status", "enabled", "passwordChanged", "sessionsRevoked", "sessionPreserved",
		"leaseGeneration", "resourceVersion", "generation", "triggerSource", "errorCode", "count",
		"slotCredentialId", "fromCredentialId", "fromModelId", "toCredentialId", "toModelId",
	} {
		switch value := input[key].(type) {
		case string:
			out[key] = strings.TrimSpace(value[:min(len(value), 256)])
		case bool, int, int64, uint64, float64:
			out[key] = value
		}
	}
	return out
}

// DispatchAuditEvents uses the existing HTTP log recorder's flush cycle. The
// log insert and delivered marker commit together, so failures/restarts leave
// pending rows and concurrent dispatchers cannot duplicate an event.
func (s *Store) DispatchAuditEvents(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin audit dispatch: %w", err)
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id::text, entry FROM audit_outbox
	  WHERE delivered_at IS NULL ORDER BY created_at, id
	  FOR UPDATE SKIP LOCKED LIMIT 100`)
	if err != nil {
		return fmt.Errorf("load pending audit events: %w", err)
	}
	type pendingEvent struct {
		id    string
		entry platform.LogEntry
	}
	events := make([]pendingEvent, 0, 100)
	for rows.Next() {
		var event pendingEvent
		var encoded []byte
		if err := rows.Scan(&event.id, &encoded); err != nil {
			rows.Close()
			return fmt.Errorf("scan audit event: %w", err)
		}
		if err := json.Unmarshal(encoded, &event.entry); err != nil {
			rows.Close()
			return fmt.Errorf("decode audit event: %w", err)
		}
		events = append(events, event)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read audit events: %w", err)
	}
	for _, event := range events {
		entry := event.entry
		if _, err := tx.Exec(ctx, `INSERT INTO system_logs
		  (audit_event_id, created_at, level, category, action, message, actor_id, actor_name,
		   resource_kind, resource_id, resource_name, status, duration_ms, remote_addr, detail)
		  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb)
		  ON CONFLICT (audit_event_id) DO NOTHING`, event.id, entry.CreatedAt, entry.Level,
			entry.Category, entry.Action, entry.Message, entry.ActorID, entry.ActorName,
			entry.ResourceKind, entry.ResourceID, entry.ResourceName, entry.Status,
			entry.DurationMS, entry.RemoteAddr, mustMapJSON(entry.Detail)); err != nil {
			return fmt.Errorf("deliver audit event: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE audit_outbox SET delivered_at = NOW() WHERE id = $1`, event.id); err != nil {
			return fmt.Errorf("mark audit event delivered: %w", err)
		}
	}
	// The system log is the long-term record; retain delivered envelopes for a
	// short recovery window without allowing the outbox to grow indefinitely.
	if _, err := tx.Exec(ctx, `DELETE FROM audit_outbox
	  WHERE delivered_at < NOW() - INTERVAL '7 days'`); err != nil {
		return fmt.Errorf("clean delivered audit events: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit audit dispatch: %w", err)
	}
	return nil
}
