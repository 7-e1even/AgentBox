package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSchemaRemovesBuiltinSkillsAndMCPServers(t *testing.T) {
	for _, expected := range []string{
		"'web-research', 'code-review', 'document-writer'",
		"'data-analysis', 'task-planner', 'support-tone'",
		"'filesystem', 'github', 'browser', 'postgres'",
		"WHERE kind IN ('runtime', 'sandbox')",
		"kind = 'skill' AND id = ANY",
		"kind = 'mcp' AND id = ANY",
		"remove-builtin-skills-and-mcp-v1",
	} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("builtin resource cleanup is missing %q", expected)
		}
	}
	for _, removed := range []string{
		`'skillIds', '["code-review", "task-planner"]'::jsonb`,
		`'mcpServerIds', '["filesystem"]'::jsonb`,
		"@modelcontextprotocol/server-filesystem",
		"@playwright/mcp@latest",
		"api.githubcopilot.com/mcp",
	} {
		if strings.Contains(schema, removed) {
			t.Fatalf("schema still provisions builtin resource %q", removed)
		}
	}
}

func TestMigrationRemovesBuiltinResourcesAndKeepsCustomSelections(t *testing.T) {
	s := newMigrationIntegrationStore(t)
	ctx := t.Context()
	runtimeID := "runtime-builtin-cleanup-test"
	// Model a legacy database before its first baseline migration. Only install
	// the resource table; the migration ledger and cleanup have not run yet.
	legacySchema, _, found := strings.Cut(schema, "CREATE TABLE IF NOT EXISTS schema_migrations")
	if !found {
		t.Fatal("baseline does not contain the migration ledger boundary")
	}
	if _, err := s.pool.Exec(ctx, legacySchema); err != nil {
		t.Fatalf("install legacy resource table: %v", err)
	}

	for _, resource := range []struct {
		id   string
		kind string
	}{
		{"web-research", "skill"},
		{"code-review", "skill"},
		{"document-writer", "skill"},
		{"data-analysis", "skill"},
		{"task-planner", "skill"},
		{"support-tone", "skill"},
		{"filesystem", "mcp"},
		{"github", "mcp"},
		{"browser", "mcp"},
		{"postgres", "mcp"},
	} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
      (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
      VALUES ($1, $2, 'default', $1, '', TRUE, '{}'::jsonb, NOW(), NOW())`,
			resource.id, resource.kind); err != nil {
			t.Fatalf("insert builtin resource %s: %v", resource.id, err)
		}
	}

	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
    (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
    VALUES ($1, 'runtime', 'default', 'Builtin cleanup test', '', TRUE,
      '{"skillIds":["web-research","custom-skill","task-planner"],"mcpServerIds":["filesystem","custom-mcp","browser"]}'::jsonb,
      NOW(), NOW())`, runtimeID); err != nil {
		t.Fatalf("insert runtime: %v", err)
	}

	if err := s.migrate(ctx); err != nil {
		t.Fatalf("migrate legacy builtin resources: %v", err)
	}
	var migrationCount int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations
    WHERE id = 'remove-builtin-skills-and-mcp-v1'`).Scan(&migrationCount); err != nil {
		t.Fatalf("count builtin cleanup migration: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("builtin cleanup migration count = %d, want 1", migrationCount)
	}

	var builtinCount int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM control_resources
    WHERE (kind = 'skill' AND id = ANY (ARRAY[
      'web-research', 'code-review', 'document-writer',
      'data-analysis', 'task-planner', 'support-tone'
    ]::text[]))
    OR (kind = 'mcp' AND id = ANY (ARRAY[
      'filesystem', 'github', 'browser', 'postgres'
    ]::text[]))`).Scan(&builtinCount); err != nil {
		t.Fatalf("count builtin resources: %v", err)
	}
	if builtinCount != 0 {
		t.Fatalf("builtin resource count = %d, want 0", builtinCount)
	}

	var skillsJSON, mcpJSON []byte
	if err := s.pool.QueryRow(ctx, `SELECT spec->'skillIds', spec->'mcpServerIds'
    FROM control_resources WHERE id = $1`, runtimeID).Scan(&skillsJSON, &mcpJSON); err != nil {
		t.Fatalf("load cleaned runtime: %v", err)
	}
	var skills, mcpServers []string
	if err := json.Unmarshal(skillsJSON, &skills); err != nil {
		t.Fatalf("decode cleaned skills: %v", err)
	}
	if err := json.Unmarshal(mcpJSON, &mcpServers); err != nil {
		t.Fatalf("decode cleaned MCP servers: %v", err)
	}
	if len(skills) != 1 || skills[0] != "custom-skill" {
		t.Fatalf("cleaned skills = %#v, want custom skill only", skills)
	}
	if len(mcpServers) != 1 || mcpServers[0] != "custom-mcp" {
		t.Fatalf("cleaned MCP servers = %#v, want custom MCP only", mcpServers)
	}
}

func TestDatabaseMigrationsDoNotReplayBaselineAfterCustomResourceRecreation(t *testing.T) {
	s := newMigrationIntegrationStore(t)
	ctx := t.Context()
	if err := s.migrate(ctx); err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	// User-created resources may reuse a former builtin ID. Project spec is an
	// additional replay sentinel: the old baseline unconditionally clears it.
	if _, err := s.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
	  VALUES
	    ('web-research', 'skill', NULL, 'Custom research', '', TRUE, '{}'::jsonb, NOW(), NOW()),
	    ('custom-project', 'project', NULL, 'Custom project', '', TRUE,
	      '{"replaySentinel":"preserved"}'::jsonb, NOW(), NOW()),
	    ('custom-runtime', 'runtime', 'custom-project', 'Custom runtime', '', TRUE,
	      '{"skillIds":["web-research"]}'::jsonb, NOW(), NOW())`); err != nil {
		t.Fatalf("insert custom resources after baseline: %v", err)
	}
	for range 2 {
		if err := s.migrate(ctx); err != nil {
			t.Fatalf("repeat migration: %v", err)
		}
	}
	assertDatabaseMigrationsRecorded(t, s)
	var name, sentinel string
	if err := s.pool.QueryRow(ctx, `SELECT name FROM control_resources
	  WHERE id = 'web-research'`).Scan(&name); err != nil {
		t.Fatalf("load recreated custom skill: %v", err)
	}
	if name != "Custom research" {
		t.Fatalf("recreated custom skill name = %q, want Custom research", name)
	}
	if err := s.pool.QueryRow(ctx, `SELECT spec->>'replaySentinel'
	  FROM control_resources WHERE id = 'custom-project'`).Scan(&sentinel); err != nil {
		t.Fatalf("load baseline replay sentinel: %v", err)
	}
	if sentinel != "preserved" {
		t.Fatalf("baseline replay sentinel = %q, want preserved", sentinel)
	}
	var selectionPreserved bool
	if err := s.pool.QueryRow(ctx, `SELECT spec->'skillIds' = '["web-research"]'::jsonb
	  FROM control_resources WHERE id = 'custom-runtime'`).Scan(&selectionPreserved); err != nil {
		t.Fatalf("load custom runtime selection: %v", err)
	}
	if !selectionPreserved {
		t.Fatal("baseline replay removed the recreated custom skill selection")
	}
}
