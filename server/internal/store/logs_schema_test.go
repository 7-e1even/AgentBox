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
