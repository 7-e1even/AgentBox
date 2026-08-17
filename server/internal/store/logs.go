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
	logDefaultPageSize = 50
	logMaxPageSize     = 200
)

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
