package store

import (
	"strings"
	"testing"
)

func TestSchemaRemovesLegacyGitHubTokenSeed(t *testing.T) {
	for _, expected := range []string{
		"WHERE variable_id <> 'github-token'",
		"AND spec->>'headers' = 'Authorization=env://GITHUB_TOKEN'",
		"DELETE FROM control_resources\nWHERE id = 'github-token'",
	} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("legacy GitHub token cleanup is missing %q", expected)
		}
	}
	if strings.Contains(schema, `'variableIds', '["github-token"]'::jsonb`) {
		t.Fatal("legacy GitHub token must not be added to environment templates")
	}
}
