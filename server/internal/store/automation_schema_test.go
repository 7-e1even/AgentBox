package store

import (
	"strings"
	"testing"
)

func TestAutomationSchemaSupportsDurablePipelineRuns(t *testing.T) {
	for _, expected := range []string{
		"'github-sha256', 'gitlab-token', 'standard-webhooks'",
		"action_type IN ('create-sandbox', 'destroy-sandbox', 'run-task')",
		"condition_template TEXT NOT NULL DEFAULT 'true'",
		"automation_run_id UUID",
		"'running', 'succeeded', 'failed', 'skipped', 'expired'",
		"result_output TEXT NOT NULL DEFAULT ''",
	} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("automation schema is missing %q", expected)
		}
	}
}
