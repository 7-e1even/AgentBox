package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentbox/internal/platform"
	"github.com/jackc/pgx/v5"
)

const logColumns = `id, created_at, level, category, action, message,
  actor_id, actor_name, resource_kind, resource_id, resource_name,
  status, duration_ms, remote_addr, detail`

const (
	logDefaultPageSize                  = 50
	logMaxPageSize                      = 200
	systemLogBestEffortMaxRows          = 100_000
	systemLogTransactionalMaxRows       = 1_000_000
	systemLogCapacityLockKey      int64 = 0x4147424c4f4753
	systemLogMaintenanceLockKey   int64 = 0x4147424c4f474d
)

const (
	expireBestEffortSystemLogsSQL = `DELETE FROM system_logs
      WHERE created_at < NOW() - INTERVAL '30 days'
        AND COALESCE(detail->>'delivery', 'best-effort') <> 'transactional'`
	expireTransactionalSystemLogsSQL = `DELETE FROM system_logs
      WHERE created_at < NOW() - INTERVAL '365 days'
        AND detail->>'delivery' = 'transactional'`
	capBestEffortSystemLogsSQL = `WITH cutoff AS MATERIALIZED (
      SELECT created_at, id FROM system_logs
      WHERE COALESCE(detail->>'delivery', 'best-effort') <> 'transactional'
      ORDER BY created_at DESC, id DESC
      OFFSET GREATEST($1 - 1, 0) LIMIT 1
    )
    DELETE FROM system_logs logs USING cutoff
    WHERE COALESCE(logs.detail->>'delivery', 'best-effort') <> 'transactional'
      AND (logs.created_at, logs.id) < (cutoff.created_at, cutoff.id)`
	capTransactionalSystemLogsSQL = `WITH cutoff AS MATERIALIZED (
      SELECT created_at, id FROM system_logs
      WHERE detail->>'delivery' = 'transactional'
      ORDER BY created_at DESC, id DESC
      OFFSET GREATEST($1 - 1, 0) LIMIT 1
    )
    DELETE FROM system_logs logs USING cutoff
    WHERE logs.detail->>'delivery' = 'transactional'
      AND (logs.created_at, logs.id) < (cutoff.created_at, cutoff.id)`
)

func lockSystemLogCapacity(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", systemLogCapacityLockKey); err != nil {
		return fmt.Errorf("lock system log capacity: %w", err)
	}
	return nil
}

func tryLockSystemLogMaintenance(ctx context.Context, tx pgx.Tx) (bool, error) {
	var locked bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", systemLogMaintenanceLockKey).Scan(&locked); err != nil {
		return false, fmt.Errorf("try lock system log maintenance: %w", err)
	}
	return locked, nil
}

func pruneSystemLogsToLimits(ctx context.Context, tx pgx.Tx, bestEffortMaxRows, transactionalMaxRows int) error {
	statements := []struct {
		query string
		args  []any
	}{
		{query: expireBestEffortSystemLogsSQL},
		{query: expireTransactionalSystemLogsSQL},
		{query: capBestEffortSystemLogsSQL, args: []any{bestEffortMaxRows}},
		{query: capTransactionalSystemLogsSQL, args: []any{transactionalMaxRows}},
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement.query, statement.args...); err != nil {
			return fmt.Errorf("prune system logs: %w", err)
		}
	}
	return nil
}

// runSystemLogMaintenance bounds system_logs in one transaction. A dedicated
// try-lock elects one replica without queueing duplicate full passes. The
// elected replica then waits for the short writer lock, so a collision with an
// active writer delays this pass instead of silently skipping it.
func (s *Store) runSystemLogMaintenance(ctx context.Context) error {
	return s.runSystemLogMaintenanceToLimits(ctx, systemLogBestEffortMaxRows, systemLogTransactionalMaxRows)
}

func (s *Store) runSystemLogMaintenanceToLimits(ctx context.Context, bestEffortMaxRows, transactionalMaxRows int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin system log maintenance: %w", err)
	}
	defer tx.Rollback(ctx)
	locked, err := tryLockSystemLogMaintenance(ctx, tx)
	if err != nil {
		return err
	}
	if !locked {
		return nil
	}
	if err := lockSystemLogCapacity(ctx, tx); err != nil {
		return err
	}
	if err := pruneSystemLogsToLimits(ctx, tx, bestEffortMaxRows, transactionalMaxRows); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit system log maintenance: %w", err)
	}
	return nil
}

// InsertLogs 批量写入系统日志。
func (s *Store) InsertLogs(ctx context.Context, entries []platform.LogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin insert system logs: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := lockSystemLogCapacity(ctx, tx); err != nil {
		return err
	}
	for _, entry := range entries {
		detail := entry.Detail
		if detail == nil {
			detail = map[string]any{}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO system_logs
      (created_at, level, category, action, message,
       actor_id, actor_name, resource_kind, resource_id, resource_name,
       status, duration_ms, remote_addr, detail)
      VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb)`,
			entry.CreatedAt, entry.Level, entry.Category, entry.Action, entry.Message,
			entry.ActorID, entry.ActorName, entry.ResourceKind, entry.ResourceID, entry.ResourceName,
			entry.Status, entry.DurationMS, entry.RemoteAddr, mustMapJSON(detail)); err != nil {
			return fmt.Errorf("insert system log: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit insert system logs: %w", err)
	}
	return nil
}

// logFilterConditions 组装 WHERE 子句与参数；返回的条件使用 $N 占位符。
func logFilterConditions(filter platform.LogFilter) ([]string, []any) {
	conditions := []string{"TRUE"}
	args := make([]any, 0, 6)
	add := func(clause string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(clause, len(args)))
	}
	if category := strings.TrimSpace(filter.Category); category != "" {
		add(`category = $%d`, category)
	}
	if level := strings.TrimSpace(filter.Level); level != "" {
		add(`level = $%d`, level)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		add(`status = $%d`, status)
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		add(`(message ILIKE $%[1]d OR action ILIKE $%[1]d OR resource_name ILIKE $%[1]d)`, "%"+query+"%")
	}
	if !filter.From.IsZero() {
		add(`created_at >= $%d`, filter.From)
	}
	if !filter.To.IsZero() {
		add(`created_at <= $%d`, filter.To)
	}
	return conditions, args
}

// ListLogs 按条件分页查询系统日志，返回条目与总数。
func (s *Store) ListLogs(ctx context.Context, filter platform.LogFilter) ([]platform.LogEntry, int, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = logDefaultPageSize
	}
	if filter.PageSize > logMaxPageSize {
		filter.PageSize = logMaxPageSize
	}
	conditions, args := logFilterConditions(filter)
	where := strings.Join(conditions, " AND ")

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM system_logs WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count system logs: %w", err)
	}

	query := `SELECT ` + logColumns + ` FROM system_logs WHERE ` + where +
		fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list system logs: %w", err)
	}
	defer rows.Close()
	entries := make([]platform.LogEntry, 0)
	for rows.Next() {
		entry, err := scanLogEntry(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan system log: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, total, rows.Err()
}

func scanLogEntry(row pgx.Row) (platform.LogEntry, error) {
	var entry platform.LogEntry
	var detailJSON []byte
	if err := row.Scan(
		&entry.ID, &entry.CreatedAt, &entry.Level, &entry.Category, &entry.Action, &entry.Message,
		&entry.ActorID, &entry.ActorName, &entry.ResourceKind, &entry.ResourceID, &entry.ResourceName,
		&entry.Status, &entry.DurationMS, &entry.RemoteAddr, &detailJSON,
	); err != nil {
		return platform.LogEntry{}, err
	}
	if err := json.Unmarshal(detailJSON, &entry.Detail); err != nil {
		return platform.LogEntry{}, fmt.Errorf("decode system log detail: %w", err)
	}
	if entry.Detail == nil {
		entry.Detail = map[string]any{}
	}
	return entry, nil
}
