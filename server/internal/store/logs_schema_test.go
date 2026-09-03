package store

import (
	"strings"
	"testing"
	"time"

	"agentbox/internal/platform"
)

func TestLogsSchemaDefinesSystemLogs(t *testing.T) {
	for _, expected := range []string{
		"CREATE TABLE IF NOT EXISTS system_logs",
		"detail JSONB NOT NULL DEFAULT '{}'::jsonb",
		"CREATE INDEX IF NOT EXISTS idx_system_logs_created",
		"CREATE INDEX IF NOT EXISTS idx_system_logs_category_created",
	} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("logs schema is missing %q", expected)
		}
	}
}

func TestSystemLogMaintenancePolicyIsKeptOffGenericJanitorStatements(t *testing.T) {
	if janitorInterval != 5*time.Minute {
		t.Fatalf("janitor interval = %s, want 5m", janitorInterval)
	}
	for _, statement := range janitorStatements {
		if strings.Contains(statement.query, "system_logs") {
			t.Fatalf("generic janitor statement bypasses transactional log maintenance: %q", statement.query)
		}
	}
	for name, query := range map[string]string{
		"best-effort expiry":   expireBestEffortSystemLogsSQL,
		"transactional expiry": expireTransactionalSystemLogsSQL,
		"best-effort cap":      capBestEffortSystemLogsSQL,
		"transactional cap":    capTransactionalSystemLogsSQL,
	} {
		if !strings.Contains(query, "system_logs") {
			t.Fatalf("%s does not target system_logs: %q", name, query)
		}
	}
	if !strings.Contains(expireBestEffortSystemLogsSQL, "INTERVAL '30 days'") ||
		!strings.Contains(expireTransactionalSystemLogsSQL, "INTERVAL '365 days'") {
		t.Fatal("system log retention windows changed")
	}
	if systemLogBestEffortMaxRows != 100_000 || systemLogTransactionalMaxRows != 1_000_000 {
		t.Fatalf("system log row limits changed: best-effort=%d transactional=%d",
			systemLogBestEffortMaxRows, systemLogTransactionalMaxRows)
	}
}

func TestLogFilterConditions(t *testing.T) {
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	conditions, args := logFilterConditions(platform.LogFilter{
		Category: "auth", Level: "warn", Status: "failed",
		Query: "登录", From: from, To: to,
	})
	joined := strings.Join(conditions, " AND ")
	for _, expected := range []string{
		"category = $1", "level = $2", "status = $3",
		"(message ILIKE $4 OR action ILIKE $4 OR resource_name ILIKE $4)",
		"created_at >= $5", "created_at <= $6",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("conditions %q missing %q", joined, expected)
		}
	}
	if len(args) != 6 || args[0] != "auth" || args[3] != "%登录%" || args[4] != from || args[5] != to {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestLogFilterConditionsEmpty(t *testing.T) {
	conditions, args := logFilterConditions(platform.LogFilter{})
	if len(conditions) != 1 || conditions[0] != "TRUE" || len(args) != 0 {
		t.Fatalf("empty filter produced conditions %v args %v", conditions, args)
	}
}
