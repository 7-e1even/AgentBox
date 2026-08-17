package store

import (
	"strings"
	"testing"
)

func TestUserSchemaMigratesUsernameLogin(t *testing.T) {
	for _, expected := range []string{
		"ALTER TABLE users ADD COLUMN IF NOT EXISTS username TEXT",
		"UPDATE users\nSET username = resolved.username",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username_lower",
	} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("user schema is missing %q", expected)
		}
	}
}
