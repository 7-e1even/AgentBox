package store

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDatabaseMigrationsAreOrderedAndChecksummed(t *testing.T) {
	migrations, err := loadDatabaseMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if len(migrations) < 2 {
		t.Fatalf("migration count = %d, want at least baseline plus one migration", len(migrations))
	}
	if migrations[0].id != "0001_legacy_schema" {
		t.Fatalf("first migration = %q, want legacy baseline", migrations[0].id)
	}
	for i, migration := range migrations {
		if strings.TrimSpace(migration.contents) == "" {
			t.Fatalf("migration %q is empty", migration.id)
		}
		if len(migration.checksum) != 64 {
			t.Fatalf("migration %q checksum length = %d, want 64", migration.id, len(migration.checksum))
		}
		if i > 0 && migrations[i-1].id >= migration.id {
			t.Fatalf("migrations are not strictly ordered: %q before %q", migrations[i-1].id, migration.id)
		}
	}
}

func TestSystemLogRetentionMigrationDefersIndexBuilds(t *testing.T) {
	migrations, err := loadDatabaseMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		if migration.id != "0009_system_log_retention" {
			continue
		}
		if strings.Contains(strings.ToUpper(migration.contents), "CREATE INDEX") {
			t.Fatal("system log retention migration would build an index inside the migration transaction")
		}
		if len(systemLogRetentionIndexes) != 2 {
			t.Fatalf("online system log index count = %d, want 2", len(systemLogRetentionIndexes))
		}
		return
	}
	t.Fatal("system log retention migration is missing")
}

func TestIndexPredicateNormalizationPreservesLiteralSemantics(t *testing.T) {
	raw := `COALESCE(detail->>'delivery', 'best-effort') <> 'transactional'`
	deparsed := `(COALESCE((detail ->> 'delivery'::text), 'best-effort'::text) <> 'transactional'::text)`
	if normalizeIndexPredicate(raw) != normalizeIndexPredicate(deparsed) {
		t.Fatal("PostgreSQL deparser formatting changed the expected predicate")
	}
	for _, wrong := range []string{
		`COALESCE(detail->>'Delivery', 'best-effort') <> 'transactional'`,
		`COALESCE(detail->>'delivery', 'best-effort') <> 'TRANSACTIONAL'`,
		`(COALESCE(detail->>'delivery', 'best-effort') <> 'transactional') AND action = 'never'`,
	} {
		if normalizeIndexPredicate(raw) == normalizeIndexPredicate(wrong) {
			t.Fatalf("normalization accepted semantically different predicate %q", wrong)
		}
	}
}

func TestSystemLogRetentionOnlineIndexesRecoverInvalidBuild(t *testing.T) {
	store := newMigrationIntegrationStore(t)
	ctx := t.Context()
	if err := store.migrate(ctx); err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	var schemaName string
	if err := store.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schemaName); err != nil {
		t.Fatal(err)
	}
	for _, spec := range systemLogRetentionIndexes {
		state, found, err := loadConcurrentIndexState(ctx, store.pool, schemaName, spec.name)
		if err != nil {
			t.Fatal(err)
		}
		if !found || !state.valid || state.tableName != spec.table {
			t.Fatalf("online index %s state = %#v found=%v", spec.name, state, found)
		}
	}

	spec := systemLogRetentionIndexes[0]
	indexIdentifier := pgx.Identifier{schemaName, spec.name}.Sanitize()
	if _, err := store.pool.Exec(ctx, "DROP INDEX CONCURRENTLY "+indexIdentifier); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO system_logs
		(level, category, action, message) VALUES
		('info', 'system', 'invalid-index-one', 'test'),
		('info', 'system', 'invalid-index-two', 'test')`); err != nil {
		t.Fatal(err)
	}
	invalidQuery := "CREATE UNIQUE INDEX CONCURRENTLY " + pgx.Identifier{spec.name}.Sanitize() +
		" ON " + pgx.Identifier{schemaName, spec.table}.Sanitize() + " ((1))"
	if _, err := store.pool.Exec(ctx, invalidQuery); err == nil {
		t.Fatal("invalid concurrent index build unexpectedly succeeded")
	}
	state, found, err := loadConcurrentIndexState(ctx, store.pool, schemaName, spec.name)
	if err != nil {
		t.Fatal(err)
	}
	if !found || state.valid {
		t.Fatalf("failed concurrent build state = %#v found=%v, want invalid index", state, found)
	}

	if err := store.migrate(ctx); err != nil {
		t.Fatalf("recover invalid concurrent index: %v", err)
	}
	state, found, err = loadConcurrentIndexState(ctx, store.pool, schemaName, spec.name)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !state.valid || !state.ready || !state.live || state.accessMethod != "btree" || state.unique ||
		state.hasExpressions || state.keyCount != len(spec.keys) || state.attributeCount != len(spec.keys) ||
		len(state.keyDefinitions) != len(spec.keys) || len(state.keyDescending) != len(spec.keys) ||
		len(state.keyNullsFirst) != len(spec.keys) ||
		normalizeIndexPredicate(state.predicate) != normalizeIndexPredicate(spec.predicate) {
		t.Fatalf("recovered concurrent index state = %#v found=%v", state, found)
	}
	for index, expected := range spec.keys {
		if normalizeIndexSQL(state.keyDefinitions[index]) != normalizeIndexSQL(expected.definition) ||
			state.keyDescending[index] != expected.descending || state.keyNullsFirst[index] != expected.nullsFirst {
			t.Fatalf("recovered concurrent index key %d state = %#v want = %#v", index, state, expected)
		}
	}
}

func TestSystemLogRetentionOnlineIndexesRecoverMissingBuildAfterMarker(t *testing.T) {
	store := newMigrationIntegrationStore(t)
	ctx := t.Context()
	if err := store.migrate(ctx); err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	var schemaName string
	if err := store.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schemaName); err != nil {
		t.Fatal(err)
	}
	spec := systemLogRetentionIndexes[0]
	indexIdentifier := pgx.Identifier{schemaName, spec.name}.Sanitize()
	if _, err := store.pool.Exec(ctx, "DROP INDEX CONCURRENTLY "+indexIdentifier); err != nil {
		t.Fatal(err)
	}
	var markerRecorded bool
	if err := store.pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM schema_migrations WHERE id = '0009_system_log_retention'
	)`).Scan(&markerRecorded); err != nil {
		t.Fatal(err)
	}
	if !markerRecorded {
		t.Fatal("system log retention marker was not committed before the simulated crash")
	}

	if err := store.migrate(ctx); err != nil {
		t.Fatalf("recover missing concurrent index after marker: %v", err)
	}
	state, found, err := loadConcurrentIndexState(ctx, store.pool, schemaName, spec.name)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !state.valid || !state.ready || !state.live {
		t.Fatalf("recovered missing concurrent index state = %#v found=%v", state, found)
	}
}

func TestSystemLogRetentionOnlineIndexesRejectValidWrongDefinition(t *testing.T) {
	store := newMigrationIntegrationStore(t)
	ctx := t.Context()
	if err := store.migrate(ctx); err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	var schemaName string
	if err := store.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schemaName); err != nil {
		t.Fatal(err)
	}
	spec := systemLogRetentionIndexes[0]
	indexIdentifier := pgx.Identifier{schemaName, spec.name}.Sanitize()
	if _, err := store.pool.Exec(ctx, "DROP INDEX CONCURRENTLY "+indexIdentifier); err != nil {
		t.Fatal(err)
	}
	wrongIndex := "CREATE INDEX CONCURRENTLY " + pgx.Identifier{spec.name}.Sanitize() +
		" ON " + pgx.Identifier{schemaName, spec.table}.Sanitize() +
		"(created_at DESC, id DESC) WHERE (" + spec.predicate + ") AND action = 'never'"
	if _, err := store.pool.Exec(ctx, wrongIndex); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(ctx); err == nil || !strings.Contains(err.Error(), "unexpected predicate") {
		t.Fatalf("migration accepted a valid index with the wrong predicate: %v", err)
	}
}

func TestSystemLogRetentionOnlineIndexesRejectValidWrongOrdering(t *testing.T) {
	store := newMigrationIntegrationStore(t)
	ctx := t.Context()
	if err := store.migrate(ctx); err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	var schemaName string
	if err := store.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schemaName); err != nil {
		t.Fatal(err)
	}
	spec := systemLogRetentionIndexes[0]
	indexIdentifier := pgx.Identifier{schemaName, spec.name}.Sanitize()
	if _, err := store.pool.Exec(ctx, "DROP INDEX CONCURRENTLY "+indexIdentifier); err != nil {
		t.Fatal(err)
	}
	wrongIndex := "CREATE INDEX CONCURRENTLY " + pgx.Identifier{spec.name}.Sanitize() +
		" ON " + pgx.Identifier{schemaName, spec.table}.Sanitize() +
		"(created_at ASC NULLS LAST, id DESC NULLS FIRST) WHERE " + spec.predicate
	if _, err := store.pool.Exec(ctx, wrongIndex); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(ctx); err == nil || !strings.Contains(err.Error(), "unexpected key ordering") {
		t.Fatalf("migration accepted a valid index with the wrong ordering: %v", err)
	}
}

func TestDatabaseMigrationsAdoptLegacySchema(t *testing.T) {
	store := newMigrationIntegrationStore(t)
	ctx := t.Context()
	if _, err := store.pool.Exec(ctx, schema); err != nil {
		t.Fatalf("install legacy schema: %v", err)
	}
	legacyServerID := uuid.NewString()
	legacyLastSeenAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Microsecond)
	if _, err := store.pool.Exec(ctx, `INSERT INTO managed_servers
	  (id, name, hostname, os, arch, credential_hash, last_seen_at, created_at, updated_at)
	  VALUES ($1, 'Legacy Worker', 'legacy-worker', 'linux', 'amd64', $2, $3::timestamptz,
	    $3::timestamptz - INTERVAL '1 day', $3::timestamptz)`, legacyServerID, hashToken(legacyServerID), legacyLastSeenAt); err != nil {
		t.Fatalf("insert legacy Worker: %v", err)
	}

	if err := store.migrate(ctx); err != nil {
		t.Fatalf("adopt legacy schema: %v", err)
	}
	assertDatabaseMigrationsRecorded(t, store)
	var hasWorkerJobActivity bool
	if err := store.pool.QueryRow(ctx, `SELECT EXISTS(
	  SELECT 1 FROM information_schema.columns
	  WHERE table_schema = current_schema() AND table_name = 'managed_servers'
	    AND column_name = 'last_job_activity_at'
	)`).Scan(&hasWorkerJobActivity); err != nil {
		t.Fatalf("inspect Worker job activity migration: %v", err)
	}
	if !hasWorkerJobActivity {
		t.Fatal("Worker job activity migration did not add managed_servers.last_job_activity_at")
	}
	var migratedActivityAt time.Time
	if err := store.pool.QueryRow(ctx, `SELECT last_job_activity_at FROM managed_servers
	  WHERE id = $1`, legacyServerID).Scan(&migratedActivityAt); err != nil {
		t.Fatalf("load migrated Worker job activity: %v", err)
	}
	if !migratedActivityAt.Equal(legacyLastSeenAt) {
		t.Fatalf("migrated Worker job activity = %s, want last heartbeat %s", migratedActivityAt, legacyLastSeenAt)
	}

	var legacyChecksum *string
	if err := store.pool.QueryRow(ctx, `SELECT checksum FROM schema_migrations
		WHERE id = 'remove-builtin-skills-and-mcp-v1'`).Scan(&legacyChecksum); err != nil {
		t.Fatalf("load legacy migration marker: %v", err)
	}
	if legacyChecksum != nil {
		t.Fatalf("legacy migration checksum = %q, want NULL", *legacyChecksum)
	}
}

func TestDatabaseMigrationsUpgradeV016Schema(t *testing.T) {
	store := newMigrationIntegrationStore(t)
	ctx := t.Context()
	// Exact fixture from git tag v0.1.6:server/internal/store/schema.sql,
	// blob 4bd72b318126614276d4901b9cbb927345499200.
	legacySQL, err := os.ReadFile("testdata/schema-v0.1.6.sql")
	if err != nil {
		t.Fatalf("read v0.1.6 schema fixture: %v", err)
	}
	if _, err := store.pool.Exec(ctx, string(legacySQL)); err != nil {
		t.Fatalf("install v0.1.6 schema: %v", err)
	}
	// Simulate a database that received the structured Worker error columns
	// before the security migration while still retaining v0.1.6 data.
	if _, err := store.pool.Exec(ctx, `ALTER TABLE worker_jobs
	  ADD COLUMN IF NOT EXISTS result_error_code TEXT NOT NULL DEFAULT '',
	  ADD COLUMN IF NOT EXISTS result_error_stage TEXT NOT NULL DEFAULT '',
	  ADD COLUMN IF NOT EXISTS result_error_retryable BOOLEAN NOT NULL DEFAULT FALSE,
	  ADD COLUMN IF NOT EXISTS result_error_details JSONB NOT NULL DEFAULT '{}'::jsonb;
	ALTER TABLE automation_runs
	  ADD COLUMN IF NOT EXISTS exit_code INTEGER,
	  ADD COLUMN IF NOT EXISTS output TEXT NOT NULL DEFAULT '',
	  ADD COLUMN IF NOT EXISTS output_truncated BOOLEAN NOT NULL DEFAULT FALSE,
	  ADD COLUMN IF NOT EXISTS cleanup_status TEXT NOT NULL DEFAULT '',
	  ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ,
	  ADD COLUMN IF NOT EXISTS error_stage TEXT NOT NULL DEFAULT '',
	  ADD COLUMN IF NOT EXISTS error_retryable BOOLEAN NOT NULL DEFAULT FALSE,
	  ADD COLUMN IF NOT EXISTS error_details JSONB NOT NULL DEFAULT '{}'::jsonb;
	ALTER TABLE automation_runs DROP CONSTRAINT IF EXISTS automation_runs_action_type_check;
	ALTER TABLE automation_runs DROP CONSTRAINT IF EXISTS automation_runs_status_check`); err != nil {
		t.Fatalf("install partial structured Worker error schema: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
	  VALUES
	    ('legacy-project', 'project', NULL, 'Existing project', '', TRUE, '{}'::jsonb, NOW(), NOW()),
	    ('legacy-custom-skill', 'skill', 'legacy-project', 'Existing custom skill', '', TRUE,
	      '{"content":"keep this custom skill"}'::jsonb, NOW(), NOW()),
	    ('legacy-runtime', 'runtime', 'legacy-project', 'Existing template', '', TRUE,
	      '{"driver":"docker","network":"restricted","skillIds":["legacy-custom-skill"],"agentTools":["codex"],"mcpServerIds":["legacy-mcp-safe","legacy-mcp-unsafe"],"customValue":"preserved"}'::jsonb,
	      NOW(), NOW()),
	    ('legacy-url-runtime', 'runtime', 'legacy-project', 'Existing URL template', '', TRUE,
	      '{"driver":"docker","network":"restricted","agentTools":["codex"],"mcpServerIds":["legacy-mcp-url-unsafe"]}'::jsonb,
	      NOW(), NOW()),
	    ('legacy-mcp-safe', 'mcp', 'legacy-project', 'Existing referenced MCP', '', TRUE,
	      '{"transport":"http","url":"https://safe.example.test/rpc","headers":" X-Api-Key = env://MCP_TOKEN \nX-Trace = secret://TRACE "}'::jsonb,
	      NOW(), NOW()),
	    ('legacy-mcp-unsafe', 'mcp', 'legacy-project', 'Existing literal MCP', '', TRUE,
	      '{"transport":"http","url":"https://unsafe.example.test/rpc","headers":"Authorization=Bearer resource-plaintext-secret"}'::jsonb,
	      NOW(), NOW()),
	    ('legacy-mcp-malformed', 'mcp', 'legacy-project', 'Existing malformed MCP', '', TRUE,
	      '{"transport":"http","url":"https://malformed.example.test/rpc","headers":{"X-Api-Key":"resource-shape-plaintext-secret"}}'::jsonb,
	      NOW(), NOW()),
	    ('legacy-mcp-url-unsafe', 'mcp', 'legacy-project', 'Existing URL credential MCP', '', TRUE,
	      '{"transport":"http","url":"https://user:url-plaintext-secret@url-unsafe.example.test/rpc?token=url-plaintext-secret#url-plaintext-secret"}'::jsonb,
	      NOW(), NOW()),
	    ('legacy-mcp-canonical', 'mcp', 'legacy-project', 'Existing canonical MCP', '', TRUE,
	      '{"transport":"http","url":"https://canonical.example.test/rpc","headers":[{"name":"X-Api-Key","valueFrom":"env://CANONICAL_TOKEN"}]}'::jsonb,
	      NOW(), NOW()),
	    ('legacy-canonical-runtime', 'runtime', 'legacy-project', 'Existing canonical template', '', TRUE,
	      '{"driver":"docker","network":"restricted","mcpServerIds":["legacy-mcp-canonical"]}'::jsonb,
	      NOW(), NOW()),
	    ('legacy-microsandbox-runtime', 'runtime', 'legacy-project', 'Existing Microsandbox template', '', TRUE,
	      '{"driver":"microsandbox","network":"restricted"}'::jsonb, NOW(), NOW()),
	    ('legacy-boxlite-runtime', 'runtime', 'legacy-project', 'Existing BoxLite template', '', TRUE,
	      '{"driver":"boxlite","network":"restricted"}'::jsonb, NOW(), NOW()),
	    ('legacy-docker-sandbox', 'sandbox', 'legacy-project', 'Existing Docker sandbox', '', TRUE,
	      '{"runtimeId":"legacy-runtime","driver":"docker","network":"restricted","status":"running","externalId":"sandbox-history-plaintext-secret","message":"sandbox-history-plaintext-secret","provisioning":{"message":"sandbox-history-plaintext-secret","extensions":[{"id":"example","output":"sandbox-history-plaintext-secret"}]},"extensionStates":[{"id":"example","status":"succeeded","output":"sandbox-history-plaintext-secret"}],"agentToolVersions":[{"tool":"codex","status":"installed","currentVersion":"sandbox-history-plaintext-secret","previousVersion":"sandbox-history-plaintext-secret"}],"agentToolOperation":{"message":"sandbox-history-plaintext-secret"},"proxyOperation":{"message":"sandbox-history-plaintext-secret"}}'::jsonb, NOW(), NOW()),
	    ('orphan-history-sandbox', 'sandbox', 'legacy-project', 'Orphaned historical sandbox', '', TRUE,
	      '{"driver":"docker","network":"egress","status":"running","externalId":"orphan-sandbox-plaintext-secret","message":"orphan-sandbox-plaintext-secret","provisioning":{"message":"orphan-sandbox-plaintext-secret"},"extensionStates":[{"id":"example","status":"succeeded","output":"orphan-sandbox-plaintext-secret"}],"agentToolVersions":[{"tool":"codex","status":"installed","currentVersion":"orphan-sandbox-plaintext-secret"}],"agentToolOperation":{"message":"orphan-sandbox-plaintext-secret"},"proxyOperation":{"message":"orphan-sandbox-plaintext-secret"}}'::jsonb, NOW(), NOW()),
	    ('legacy-url-sandbox', 'sandbox', 'legacy-project', 'Existing URL sandbox', '', TRUE,
	      '{"runtimeId":"legacy-url-runtime","driver":"docker","network":"restricted","status":"running"}'::jsonb, NOW(), NOW()),
	    ('legacy-microsandbox-sandbox', 'sandbox', 'legacy-project', 'Existing Microsandbox sandbox', '', TRUE,
	      '{"runtimeId":"legacy-microsandbox-runtime","network":"restricted"}'::jsonb, NOW(), NOW()),
	    ('legacy-boxlite-sandbox', 'sandbox', 'legacy-project', 'Existing BoxLite sandbox', '', TRUE,
	      '{"runtimeId":"legacy-boxlite-runtime","network":"restricted"}'::jsonb, NOW(), NOW()),
	    ('legacy-canonical-sandbox', 'sandbox', 'legacy-project', 'Existing canonical sandbox', '', TRUE,
	      '{"runtimeId":"legacy-canonical-runtime","driver":"docker","network":"restricted"}'::jsonb, NOW(), NOW())`); err != nil {
		t.Fatalf("insert v0.1.6 resources: %v", err)
	}
	serverID := uuid.NewString()
	lastSeenAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	if _, err := store.pool.Exec(ctx, `INSERT INTO managed_servers
	  (id, name, hostname, os, arch, credential_hash, last_seen_at, created_at, updated_at)
	  VALUES ($1, 'Existing Worker', 'existing-worker', 'linux', 'amd64', $2, $3, $3, $3)`,
		serverID, hashToken(serverID), lastSeenAt); err != nil {
		t.Fatalf("insert v0.1.6 Worker: %v", err)
	}
	dockerJobID := uuid.NewString()
	microsandboxJobID := uuid.NewString()
	boxliteJobID := uuid.NewString()
	completedJobID := uuid.NewString()
	canonicalJobID := uuid.NewString()
	if _, err := store.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, resource_id, action, status, payload, created_at, updated_at)
	  VALUES
	    ($1, $5, 'legacy-docker-sandbox', 'create-sandbox', 'pending',
	      '{"driver":"docker","network":"restricted","mcpServers":[{"id":"legacy-mcp-safe","spec":{"transport":"http","url":"https://safe.example.test/rpc","headers":" X-Api-Key = env://MCP_TOKEN "}},{"id":"legacy-mcp-unsafe","spec":{"transport":"http","url":"https://unsafe.example.test/rpc","headers":"Authorization=Bearer pending-job-plaintext-secret"}},{"id":"legacy-mcp-url-unsafe","spec":{"transport":"http","url":"https://user:job-url-plaintext-secret@job-url.example.test/rpc?token=job-url-plaintext-secret"}}]}'::jsonb,
	      NOW(), NOW()),
	    ($2, $5, 'legacy-microsandbox-sandbox', 'start-sandbox', 'pending',
	      '{"driver":"microsandbox","network":"restricted"}'::jsonb, NOW(), NOW()),
	    ($3, $5, 'legacy-boxlite-sandbox', 'restart-sandbox', 'leased',
	      '{"driver":"boxlite","network":"restricted","mcpServers":[{"id":"legacy-mcp-unsafe","spec":{"transport":"http","url":"https://unsafe.example.test/rpc","headers":"Authorization=Bearer leased-job-plaintext-secret"}}]}'::jsonb,
	      NOW(), NOW()),
	    ($4, $5, 'legacy-docker-sandbox', 'restart-sandbox', 'failed',
	      '{"driver":"docker","network":"restricted","mcpServers":[{"id":"legacy-mcp-unsafe","spec":{"transport":"http","url":"https://unsafe.example.test/rpc","headers":[{"name":"X-Api-Key","valueFrom":"terminal-job-plaintext-secret"}]}},{"id":"legacy-mcp-malformed","spec":{"transport":"http","url":"https://malformed.example.test/rpc","headers":{"X-Api-Key":"terminal-shape-plaintext-secret"}}}]}'::jsonb,
	      NOW(), NOW()),
	    ($6, $5, 'legacy-canonical-sandbox', 'restart-sandbox', 'succeeded',
	      '{"driver":"docker","network":"restricted","mcpServers":[{"id":"legacy-mcp-canonical","spec":{"transport":"http","url":"https://canonical.example.test/rpc","headers":[{"name":"X-Api-Key","valueFrom":"env://CANONICAL_TOKEN"}]}}]}'::jsonb,
	      NOW(), NOW())`,
		dockerJobID, microsandboxJobID, boxliteJobID, completedJobID, serverID, canonicalJobID); err != nil {
		t.Fatalf("insert v0.1.6 Worker jobs: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE worker_jobs SET
	  result_message = 'job-history-plaintext-secret',
	  result_output = 'job-history-plaintext-secret',
	  external_id = 'job-history-plaintext-secret',
	  result_error_code = 'job-code-plaintext-secret',
	  result_error_stage = 'job-stage-plaintext-secret',
	  result_error_retryable = TRUE,
	  result_error_details = '{"raw":"job-error-plaintext-secret"}'::jsonb,
	  progress = '{"message":"job-history-plaintext-secret","extensions":[{"id":"example","output":"job-history-plaintext-secret"}],"agentTools":[{"tool":"codex","message":"job-history-plaintext-secret"}]}'::jsonb
	  WHERE id = ANY($1::uuid[])`, []string{
		dockerJobID, microsandboxJobID, boxliteJobID, completedJobID, canonicalJobID,
	}); err != nil {
		t.Fatalf("seed historical Worker result sinks: %v", err)
	}
	automationRunID := uuid.NewString()
	if _, err := store.pool.Exec(ctx, `INSERT INTO automation_runs
	  (id, project_id, automation_name, action_type, template_id, template_name,
	   trigger_source, auth_mode, payload_sha256, payload_bytes, status, sandbox_id,
	   worker_job_id, error_code, error_message, output, error_stage, error_retryable,
	   error_details, provisioning, received_at)
	  VALUES ($1, 'legacy-project', 'Legacy automation', 'create-sandbox', 'legacy-runtime',
	    'Existing template', 'manual-test', 'bearer', decode(repeat('00', 32), 'hex'), 0,
	    'failed', 'legacy-docker-sandbox', $2, 'automation-code-plaintext-secret',
	    'automation-history-plaintext-secret', 'retained-output-plaintext-secret',
	    'automation-stage-plaintext-secret', TRUE,
	    '{"raw":"automation-error-plaintext-secret"}'::jsonb,
	    '{"message":"automation-history-plaintext-secret","extensions":[{"output":"automation-history-plaintext-secret"}]}'::jsonb,
		NOW())`, automationRunID, completedJobID); err != nil {
		t.Fatalf("seed historical automation Worker sinks: %v", err)
	}
	orphanAutomationRunID := uuid.NewString()
	if _, err := store.pool.Exec(ctx, `INSERT INTO automation_runs
	  (id, project_id, automation_name, action_type, template_id, template_name,
	   trigger_source, auth_mode, payload_sha256, payload_bytes, status,
	   error_code, error_message, error_stage, error_retryable, error_details,
	   provisioning, received_at)
	  VALUES ($1, 'legacy-project', 'Orphaned historical automation', 'create-sandbox',
	    'legacy-runtime', 'Existing template', 'manual-test', 'bearer',
	    decode(repeat('00', 32), 'hex'), 0, 'failed', 'orphan-code-plaintext-secret',
	    'orphan-automation-plaintext-secret',
	    'orphan-stage-plaintext-secret', TRUE,
	    '{"raw":"orphan-error-plaintext-secret"}'::jsonb,
	    '{"message":"orphan-automation-plaintext-secret","extensions":[{"output":"orphan-automation-plaintext-secret"}]}'::jsonb,
	    NOW())`, orphanAutomationRunID); err != nil {
		t.Fatalf("seed orphaned historical automation sinks: %v", err)
	}
	legacyActionRunID := uuid.NewString()
	legacyStatusRunID := uuid.NewString()
	if _, err := store.pool.Exec(ctx, `INSERT INTO automation_runs
	  (id, project_id, automation_name, action_type, template_id, template_name,
	   trigger_source, auth_mode, payload_sha256, payload_bytes, status,
	   error_message, output, provisioning, received_at)
	  VALUES
	    ($1, 'legacy-project', 'Removed run-task automation', 'run-task', 'legacy-runtime',
	      'Existing template', 'manual-test', 'bearer', decode(repeat('00', 32), 'hex'), 0,
	      'running', 'legacy-action-plaintext-secret', 'legacy-output-plaintext-secret',
	      '{"message":"legacy-action-plaintext-secret"}'::jsonb, NOW()),
	    ($2, 'legacy-project', 'Removed skipped automation', 'create-sandbox', 'legacy-runtime',
	      'Existing template', 'manual-test', 'bearer', decode(repeat('00', 32), 'hex'), 0,
	      'skipped', 'legacy-status-plaintext-secret', '',
	      '{"message":"legacy-status-plaintext-secret"}'::jsonb, NOW())`,
		legacyActionRunID, legacyStatusRunID); err != nil {
		t.Fatalf("seed retired automation runs: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `ALTER TABLE automation_runs
	  ADD CONSTRAINT automation_runs_action_type_check
	  CHECK (action_type = 'create-sandbox') NOT VALID;
	ALTER TABLE automation_runs ADD CONSTRAINT automation_runs_status_check
	  CHECK (status IN ('evaluating', 'queued', 'provisioning', 'succeeded', 'failed')) NOT VALID`); err != nil {
		t.Fatalf("restore current Automation run constraints: %v", err)
	}

	for range 2 {
		if err := store.migrate(ctx); err != nil {
			t.Fatalf("upgrade v0.1.6 database: %v", err)
		}
	}
	assertDatabaseMigrationsRecorded(t, store)
	var resourcesPreserved bool
	if err := store.pool.QueryRow(ctx, `SELECT
	  EXISTS (SELECT 1 FROM control_resources WHERE id = 'legacy-project' AND name = 'Existing project')
	  AND EXISTS (SELECT 1 FROM control_resources WHERE id = 'legacy-custom-skill'
	    AND spec->>'content' = 'keep this custom skill')
	  AND EXISTS (SELECT 1 FROM control_resources WHERE id = 'legacy-runtime'
	    AND name = 'Existing template' AND spec->>'customValue' = 'preserved'
	    AND spec->'skillIds' = '["legacy-custom-skill"]'::jsonb)`).Scan(&resourcesPreserved); err != nil {
		t.Fatalf("load upgraded v0.1.6 resources: %v", err)
	}
	if !resourcesPreserved {
		t.Fatal("upgrade did not preserve the existing project, custom skill, or template")
	}
	var firstHeaderName, firstHeaderReference, secondHeaderReference, canonicalHeaderReference string
	var unsafeResourceHasHeaders, malformedResourceHasHeaders bool
	if err := store.pool.QueryRow(ctx, `SELECT
	    (SELECT spec#>>'{headers,0,name}' FROM control_resources WHERE id = 'legacy-mcp-safe'),
	    (SELECT spec#>>'{headers,0,valueFrom}' FROM control_resources WHERE id = 'legacy-mcp-safe'),
	    (SELECT spec#>>'{headers,1,valueFrom}' FROM control_resources WHERE id = 'legacy-mcp-safe'),
	    (SELECT spec ? 'headers' FROM control_resources WHERE id = 'legacy-mcp-unsafe'),
	    (SELECT spec ? 'headers' FROM control_resources WHERE id = 'legacy-mcp-malformed'),
	    (SELECT spec#>>'{headers,0,valueFrom}' FROM control_resources WHERE id = 'legacy-mcp-canonical')`).Scan(
		&firstHeaderName, &firstHeaderReference, &secondHeaderReference, &unsafeResourceHasHeaders,
		&malformedResourceHasHeaders, &canonicalHeaderReference,
	); err != nil {
		t.Fatalf("load migrated MCP resource headers: %v", err)
	}
	if firstHeaderName != "X-Api-Key" || firstHeaderReference != "env://MCP_TOKEN" ||
		secondHeaderReference != "secret://TRACE" || unsafeResourceHasHeaders || malformedResourceHasHeaders ||
		canonicalHeaderReference != "env://CANONICAL_TOKEN" {
		t.Fatalf("migrated MCP resource headers = %q/%q/%q unsafe=%v malformed=%v canonical=%q",
			firstHeaderName, firstHeaderReference, secondHeaderReference, unsafeResourceHasHeaders,
			malformedResourceHasHeaders, canonicalHeaderReference)
	}
	var unsafeURLPresent, unsafeURLEnabled bool
	var safeURL string
	if err := store.pool.QueryRow(ctx, `SELECT
	    (SELECT spec ? 'url' FROM control_resources WHERE id = 'legacy-mcp-url-unsafe'),
	    (SELECT enabled FROM control_resources WHERE id = 'legacy-mcp-url-unsafe'),
	    (SELECT spec->>'url' FROM control_resources WHERE id = 'legacy-mcp-safe')`).Scan(
		&unsafeURLPresent, &unsafeURLEnabled, &safeURL,
	); err != nil {
		t.Fatalf("load migrated MCP URLs: %v", err)
	}
	if unsafeURLPresent || unsafeURLEnabled || safeURL != "https://safe.example.test/rpc" {
		t.Fatalf("migrated MCP URLs: unsafe present=%v enabled=%v safe=%q",
			unsafeURLPresent, unsafeURLEnabled, safeURL)
	}
	for _, sandboxID := range []string{"legacy-docker-sandbox", "legacy-url-sandbox", "legacy-boxlite-sandbox"} {
		var pending bool
		var revision, generation int64
		if err := store.pool.QueryRow(ctx, `SELECT
	      COALESCE((spec->>'capabilitiesPendingRestart')::boolean, FALSE),
	      COALESCE((spec->>'capabilityRevision')::bigint, 0), generation
	      FROM control_resources WHERE id = $1`, sandboxID).Scan(&pending, &revision, &generation); err != nil {
			t.Fatalf("load migrated Sandbox capability state for %s: %v", sandboxID, err)
		}
		if !pending || revision != 1 || generation != 1 {
			t.Fatalf("migrated Sandbox %s state = pending %v, revision %d, generation %d",
				sandboxID, pending, revision, generation)
		}
	}
	var canonicalPending bool
	var canonicalRevision, canonicalGeneration int64
	if err := store.pool.QueryRow(ctx, `SELECT
	    COALESCE((spec->>'capabilitiesPendingRestart')::boolean, FALSE),
	    COALESCE((spec->>'capabilityRevision')::bigint, 0), generation
	    FROM control_resources WHERE id = 'legacy-canonical-sandbox'`).Scan(
		&canonicalPending, &canonicalRevision, &canonicalGeneration,
	); err != nil {
		t.Fatalf("load canonical Sandbox capability state: %v", err)
	}
	if canonicalPending || canonicalRevision != 0 || canonicalGeneration != 1 {
		t.Fatalf("canonical Sandbox was unnecessarily marked pending: pending=%v revision=%d generation=%d",
			canonicalPending, canonicalRevision, canonicalGeneration)
	}
	var pendingHeaderType, pendingHeaderReference string
	var pendingUnsafeHasHeaders, leasedUnsafeHasHeaders, terminalUnsafeHasHeaders, terminalShapeHasHeaders bool
	var canonicalJobHeaderReference string
	if err := store.pool.QueryRow(ctx, `SELECT
	    jsonb_typeof((SELECT payload#>'{mcpServers,0,spec,headers}' FROM worker_jobs WHERE id = $1)),
	    (SELECT payload#>>'{mcpServers,0,spec,headers,0,valueFrom}' FROM worker_jobs WHERE id = $1),
	    (SELECT (payload#>'{mcpServers,1,spec}') ? 'headers' FROM worker_jobs WHERE id = $1),
	    (SELECT (payload#>'{mcpServers,0,spec}') ? 'headers' FROM worker_jobs WHERE id = $2),
	    (SELECT (payload#>'{mcpServers,0,spec}') ? 'headers' FROM worker_jobs WHERE id = $3),
	    (SELECT (payload#>'{mcpServers,1,spec}') ? 'headers' FROM worker_jobs WHERE id = $3),
	    (SELECT payload#>>'{mcpServers,0,spec,headers,0,valueFrom}' FROM worker_jobs WHERE id = $4)`,
		dockerJobID, boxliteJobID, completedJobID, canonicalJobID).Scan(
		&pendingHeaderType, &pendingHeaderReference, &pendingUnsafeHasHeaders,
		&leasedUnsafeHasHeaders, &terminalUnsafeHasHeaders, &terminalShapeHasHeaders,
		&canonicalJobHeaderReference,
	); err != nil {
		t.Fatalf("load migrated Worker job MCP headers: %v", err)
	}
	if pendingHeaderType != "array" || pendingHeaderReference != "env://MCP_TOKEN" ||
		pendingUnsafeHasHeaders || leasedUnsafeHasHeaders || terminalUnsafeHasHeaders || terminalShapeHasHeaders ||
		canonicalJobHeaderReference != "env://CANONICAL_TOKEN" {
		t.Fatalf("migrated Worker headers = %q/%q unsafe pending=%v leased=%v terminal=%v shape=%v canonical=%q",
			pendingHeaderType, pendingHeaderReference, pendingUnsafeHasHeaders,
			leasedUnsafeHasHeaders, terminalUnsafeHasHeaders, terminalShapeHasHeaders, canonicalJobHeaderReference)
	}
	var unsafeURLDefinitionPresent bool
	if err := store.pool.QueryRow(ctx, `SELECT payload::text LIKE '%legacy-mcp-url-unsafe%'
	    FROM worker_jobs WHERE id = $1`, dockerJobID).Scan(&unsafeURLDefinitionPresent); err != nil {
		t.Fatalf("load migrated Worker job MCP URLs: %v", err)
	}
	if unsafeURLDefinitionPresent {
		t.Fatal("migration retained an unsafe MCP URL definition in a Worker payload")
	}
	var migratedJobCode, migratedJobStage string
	var migratedJobRetryable, migratedJobTimedOut bool
	if err := store.pool.QueryRow(ctx, `SELECT result_error_code, result_error_stage,
	    result_error_retryable, result_timed_out FROM worker_jobs WHERE id = $1`, completedJobID).Scan(
		&migratedJobCode, &migratedJobStage, &migratedJobRetryable, &migratedJobTimedOut,
	); err != nil {
		t.Fatalf("load migrated Worker error metadata: %v", err)
	}
	if migratedJobCode != "restart_sandbox_failed" || migratedJobStage != "restart" ||
		migratedJobRetryable || migratedJobTimedOut {
		t.Fatalf("migrated Worker error = %q/%q retryable=%v timedOut=%v",
			migratedJobCode, migratedJobStage, migratedJobRetryable, migratedJobTimedOut)
	}
	var orphanSandboxSpec, orphanSandboxExternalID, orphanSandboxMessage string
	if err := store.pool.QueryRow(ctx, `SELECT spec::text, spec->>'externalId', spec->>'message'
	    FROM control_resources WHERE id = 'orphan-history-sandbox'`).Scan(
		&orphanSandboxSpec, &orphanSandboxExternalID, &orphanSandboxMessage,
	); err != nil {
		t.Fatalf("load orphaned historical Sandbox sinks: %v", err)
	}
	if strings.Contains(orphanSandboxSpec, "plaintext-secret") ||
		orphanSandboxExternalID != "agentbox-orphan-history-sandbox" ||
		orphanSandboxMessage != "Historical Worker diagnostic removed during security migration" {
		t.Fatalf("orphaned Sandbox history was not safely migrated: %s", orphanSandboxSpec)
	}
	for _, removedField := range []string{
		"provisioning", "extensionStates", "agentToolVersions", "agentToolOperation", "proxyOperation",
	} {
		if strings.Contains(orphanSandboxSpec, `"`+removedField+`"`) {
			t.Fatalf("orphaned Sandbox retained %s: %s", removedField, orphanSandboxSpec)
		}
	}
	var orphanAutomationCode, orphanAutomationMessage, orphanAutomationStage string
	var orphanAutomationDetails, orphanAutomationProvisioning string
	var orphanAutomationRetryable bool
	var orphanAutomationJobMissing, orphanAutomationSandboxMissing bool
	if err := store.pool.QueryRow(ctx, `SELECT error_code, error_message, error_stage,
	    error_retryable, error_details::text, provisioning::text,
	    worker_job_id IS NULL, sandbox_id IS NULL
	    FROM automation_runs WHERE id = $1`, orphanAutomationRunID).Scan(
		&orphanAutomationCode, &orphanAutomationMessage, &orphanAutomationStage,
		&orphanAutomationRetryable, &orphanAutomationDetails, &orphanAutomationProvisioning,
		&orphanAutomationJobMissing, &orphanAutomationSandboxMissing,
	); err != nil {
		t.Fatalf("load orphaned historical Automation sinks: %v", err)
	}
	if !orphanAutomationJobMissing || !orphanAutomationSandboxMissing ||
		orphanAutomationCode != "worker_failed" || orphanAutomationStage != "create" ||
		orphanAutomationRetryable ||
		orphanAutomationMessage != "Historical Worker diagnostic removed during security migration" ||
		orphanAutomationDetails != "{}" || orphanAutomationProvisioning != "{}" {
		t.Fatalf("orphaned Automation history = code/stage %q/%q retryable=%v message %q details %s provisioning %s jobMissing=%v sandboxMissing=%v",
			orphanAutomationCode, orphanAutomationStage, orphanAutomationRetryable,
			orphanAutomationMessage, orphanAutomationDetails, orphanAutomationProvisioning,
			orphanAutomationJobMissing, orphanAutomationSandboxMissing)
	}
	var retiredAutomationRuns int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM automation_runs
	    WHERE id = ANY($1::uuid[])`, []string{legacyActionRunID, legacyStatusRunID}).Scan(
		&retiredAutomationRuns,
	); err != nil {
		t.Fatalf("count retired Automation runs: %v", err)
	}
	if retiredAutomationRuns != 0 {
		t.Fatalf("migration retained %d retired Automation runs", retiredAutomationRuns)
	}
	var legacyAutomationColumns int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
	    WHERE table_schema = current_schema() AND table_name = 'automation_runs'
	      AND column_name = ANY($1::text[])`, []string{
		"exit_code", "output", "output_truncated", "cleanup_status", "expires_at",
	}).Scan(&legacyAutomationColumns); err != nil {
		t.Fatalf("inspect retired Automation run columns: %v", err)
	}
	if legacyAutomationColumns != 0 {
		t.Fatalf("migration retained %d retired Automation run columns", legacyAutomationColumns)
	}
	var plaintextCopies int
	if err := store.pool.QueryRow(ctx, `SELECT
	    (SELECT count(*) FROM control_resources WHERE spec::text LIKE '%plaintext-secret%') +
	    (SELECT count(*) FROM worker_jobs WHERE payload::text LIKE '%plaintext-secret%'
	      OR result_message LIKE '%plaintext-secret%' OR result_output LIKE '%plaintext-secret%'
	      OR external_id LIKE '%plaintext-secret%' OR progress::text LIKE '%plaintext-secret%'
	      OR result_error_code LIKE '%plaintext-secret%' OR result_error_stage LIKE '%plaintext-secret%'
	      OR result_error_details::text LIKE '%plaintext-secret%') +
	    (SELECT count(*) FROM automation_runs WHERE error_message LIKE '%plaintext-secret%'
	      OR error_code LIKE '%plaintext-secret%' OR error_stage LIKE '%plaintext-secret%'
	      OR error_details::text LIKE '%plaintext-secret%' OR provisioning::text LIKE '%plaintext-secret%') +
	    (SELECT count(*) FROM system_logs WHERE message LIKE '%plaintext-secret%'
	      OR detail::text LIKE '%plaintext-secret%')`).Scan(&plaintextCopies); err != nil {
		t.Fatalf("scan migrated plaintext MCP headers: %v", err)
	}
	if plaintextCopies != 0 {
		t.Fatalf("migration retained %d plaintext MCP header copies", plaintextCopies)
	}
	for _, expected := range []struct {
		id      string
		network string
	}{
		{id: "legacy-runtime", network: "egress"},
		{id: "legacy-url-runtime", network: "egress"},
		{id: "legacy-microsandbox-runtime", network: "egress"},
		{id: "legacy-boxlite-runtime", network: "restricted"},
		{id: "legacy-canonical-runtime", network: "egress"},
		{id: "legacy-docker-sandbox", network: "egress"},
		{id: "legacy-url-sandbox", network: "egress"},
		{id: "legacy-microsandbox-sandbox", network: "egress"},
		{id: "legacy-boxlite-sandbox", network: "restricted"},
		{id: "legacy-canonical-sandbox", network: "egress"},
	} {
		var network string
		if err := store.pool.QueryRow(ctx, `SELECT spec->>'network' FROM control_resources WHERE id = $1`, expected.id).Scan(&network); err != nil {
			t.Fatalf("load migrated network for %s: %v", expected.id, err)
		}
		if network != expected.network {
			t.Fatalf("migrated network for %s = %q, want %q", expected.id, network, expected.network)
		}
	}
	for _, expected := range []struct {
		id      string
		network string
	}{
		{id: dockerJobID, network: "egress"},
		{id: microsandboxJobID, network: "egress"},
		{id: boxliteJobID, network: "restricted"},
		{id: completedJobID, network: "restricted"},
		{id: canonicalJobID, network: "restricted"},
	} {
		var network string
		if err := store.pool.QueryRow(ctx, `SELECT payload->>'network' FROM worker_jobs WHERE id = $1`, expected.id).Scan(&network); err != nil {
			t.Fatalf("load migrated Worker job network for %s: %v", expected.id, err)
		}
		if network != expected.network {
			t.Fatalf("migrated Worker job network for %s = %q, want %q", expected.id, network, expected.network)
		}
	}
	var activityAt time.Time
	if err := store.pool.QueryRow(ctx, `SELECT last_job_activity_at
	  FROM managed_servers WHERE id = $1`, serverID).Scan(&activityAt); err != nil {
		t.Fatalf("load upgraded v0.1.6 Worker activity: %v", err)
	}
	if !activityAt.Equal(lastSeenAt) {
		t.Fatalf("upgraded Worker activity = %s, want %s", activityAt, lastSeenAt)
	}
}

func TestDatabaseMigrationsRejectChangedChecksum(t *testing.T) {
	store := newMigrationIntegrationStore(t)
	ctx := t.Context()
	if err := store.migrate(ctx); err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE schema_migrations SET checksum = 'changed'
		WHERE id = '0001_legacy_schema'`); err != nil {
		t.Fatalf("change migration checksum: %v", err)
	}

	err := store.migrate(ctx)
	if err == nil || !strings.Contains(err.Error(), "checksum does not match") {
		t.Fatalf("migrate with changed checksum: err = %v, want checksum mismatch", err)
	}
}

func TestDatabaseMigrationsSerializeConcurrentFirstStart(t *testing.T) {
	store := newMigrationIntegrationStore(t)
	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- store.migrate(t.Context()) }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent migration: %v", err)
		}
	}
	assertDatabaseMigrationsRecorded(t, store)
}

func TestDatabaseMigrationLockWaitDoesNotLeaveAnOpenTransaction(t *testing.T) {
	store := newMigrationIntegrationStore(t)
	ctx := t.Context()
	holder, err := store.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	holderLocked := true
	defer func() {
		if holderLocked {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = holder.Exec(cleanupCtx, "SELECT pg_advisory_unlock($1)", databaseMigrationLockKey)
			cancel()
		}
		holder.Release()
	}()
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", databaseMigrationLockKey); err != nil {
		t.Fatal(err)
	}

	waiter, err := store.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waiterReleased := false
	defer func() {
		if !waiterReleased {
			waiter.Release()
		}
	}()
	var waiterPID int
	if err := waiter.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&waiterPID); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, 250*time.Millisecond)
	err = waitForDatabaseMigrationLock(waitCtx, waiter)
	cancelWait()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("migration lock wait error = %v, want deadline exceeded", err)
	}
	var state string
	var noOpenTransaction, notWaitingOnLock bool
	if err := store.pool.QueryRow(ctx, `SELECT state, xact_start IS NULL,
		COALESCE(wait_event, '') <> 'AdvisoryLock'
		FROM pg_stat_activity WHERE pid = $1`, waiterPID).Scan(
		&state, &noOpenTransaction, &notWaitingOnLock,
	); err != nil {
		t.Fatal(err)
	}
	if state != "idle" || !noOpenTransaction || !notWaitingOnLock {
		t.Fatalf("waiting migration connection state=%q noOpenTransaction=%v notWaitingOnLock=%v",
			state, noOpenTransaction, notWaitingOnLock)
	}
	var unlocked bool
	if err := holder.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", databaseMigrationLockKey).Scan(&unlocked); err != nil {
		t.Fatal(err)
	}
	if !unlocked {
		t.Fatal("migration holder lock was not released")
	}
	holderLocked = false
	if err := waitForDatabaseMigrationLock(ctx, waiter); err != nil {
		t.Fatalf("acquire migration lock after release: %v", err)
	}
	err = releaseDatabaseMigrationLock(waiter)
	waiterReleased = true
	if err != nil {
		t.Fatal(err)
	}
}

func assertDatabaseMigrationsRecorded(t *testing.T, store *Store) {
	t.Helper()
	migrations, err := loadDatabaseMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	for _, migration := range migrations {
		var checksum string
		if err := store.pool.QueryRow(t.Context(), `SELECT checksum FROM schema_migrations WHERE id = $1`, migration.id).Scan(&checksum); err != nil {
			t.Fatalf("load migration %s: %v", migration.id, err)
		}
		if checksum != migration.checksum {
			t.Fatalf("migration %s checksum = %q, want %q", migration.id, checksum, migration.checksum)
		}
	}
}

func newMigrationIntegrationStore(t *testing.T) *Store {
	t.Helper()
	databaseURL := newIntegrationDatabaseURL(t)
	storeConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse store database URL: %v", err)
	}
	storeConfig.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(t.Context(), storeConfig)
	if err != nil {
		t.Fatalf("open isolated migration store: %v", err)
	}
	if err := pool.Ping(t.Context()); err != nil {
		pool.Close()
		t.Fatalf("connect isolated migration store: %v", err)
	}
	t.Cleanup(pool.Close)
	return &Store{pool: pool}
}

// Each integration test gets its own schema. Callers register their pool or
// Store cleanup after this helper returns, so it closes before DROP SCHEMA.
func newIntegrationDatabaseURL(t *testing.T) string {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("AGENTBOX_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("AGENTBOX_TEST_DATABASE_URL is not set; skipping PostgreSQL integration test")
	}

	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse integration database URL: %v", err)
	}
	adminPool, err := pgxpool.NewWithConfig(t.Context(), adminConfig)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	if err := adminPool.Ping(t.Context()); err != nil {
		adminPool.Close()
		t.Fatalf("connect integration database: %v", err)
	}
	t.Cleanup(adminPool.Close)

	schemaName := "agentbox_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	schemaIdentifier := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(t.Context(), "CREATE SCHEMA "+schemaIdentifier); err != nil {
		t.Fatalf("create integration schema: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := adminPool.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+schemaIdentifier+" CASCADE"); err != nil {
			t.Errorf("drop integration schema: %v", err)
		}
	})

	// Put search_path in the actual DSN, rather than only in a parsed config,
	// so tests reopening pool.Config().ConnString() stay in this same schema.
	if strings.HasPrefix(databaseURL, "postgres://") || strings.HasPrefix(databaseURL, "postgresql://") {
		parsed, err := url.Parse(databaseURL)
		if err != nil {
			t.Fatalf("parse integration database URI: %v", err)
		}
		query := parsed.Query()
		query.Set("search_path", schemaName)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return databaseURL + " search_path=" + schemaName
}
