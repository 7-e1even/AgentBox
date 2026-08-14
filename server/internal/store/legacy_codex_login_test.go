package store

import (
	"strings"
	"testing"
)

func TestLegacyCodexLoginStateIsRemoved(t *testing.T) {
	for _, expected := range []string{
		`DELETE FROM worker_jobs WHERE action = 'login-agent'`,
		`spec = spec - 'loginStatus' - 'loginMessage' - 'loginTool'`,
	} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("schema is missing Codex login cleanup %q", expected)
		}
	}
}
