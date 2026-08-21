package store

import (
	"strings"
	"testing"
)

func TestAutomationSchemaSupportsTemplateSandboxRuns(t *testing.T) {
	for _, expected := range []string{
		"'github-sha256', 'gitlab-token', 'standard-webhooks'",
		"CHECK (action_type = 'create-sandbox')",
		"automation_run_id UUID",
		"'evaluating', 'queued', 'provisioning', 'succeeded', 'failed'",
	} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("automation schema is missing %q", expected)
		}
	}
	for _, removed := range []string{"run-codex", "run-task", "destroy-sandbox", "CREATE TABLE IF NOT EXISTS codex_threads"} {
		if strings.Contains(schema, removed) {
			t.Fatalf("automation schema still contains removed feature %q", removed)
		}
	}
}
