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
	if _, err := store.pool.Exec(ctx, `INSERT INTO control_resources
	  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
	  VALUES
	    ('legacy-project', 'project', NULL, 'Existing project', '', TRUE, '{}'::jsonb, NOW(), NOW()),
	    ('legacy-custom-skill', 'skill', 'legacy-project', 'Existing custom skill', '', TRUE,
	      '{"content":"keep this custom skill"}'::jsonb, NOW(), NOW()),
	    ('legacy-runtime', 'runtime', 'legacy-project', 'Existing template', '', TRUE,
	      '{"driver":"docker","network":"restricted","skillIds":["legacy-custom-skill"],"customValue":"preserved"}'::jsonb,
	      NOW(), NOW()),
	    ('legacy-microsandbox-runtime', 'runtime', 'legacy-project', 'Existing Microsandbox template', '', TRUE,
	      '{"driver":"microsandbox","network":"restricted"}'::jsonb, NOW(), NOW()),
	    ('legacy-boxlite-runtime', 'runtime', 'legacy-project', 'Existing BoxLite template', '', TRUE,
	      '{"driver":"boxlite","network":"restricted"}'::jsonb, NOW(), NOW()),
	    ('legacy-docker-sandbox', 'sandbox', 'legacy-project', 'Existing Docker sandbox', '', TRUE,
	      '{"runtimeId":"legacy-runtime","driver":"docker","network":"restricted"}'::jsonb, NOW(), NOW()),
	    ('legacy-microsandbox-sandbox', 'sandbox', 'legacy-project', 'Existing Microsandbox sandbox', '', TRUE,
	      '{"runtimeId":"legacy-microsandbox-runtime","network":"restricted"}'::jsonb, NOW(), NOW()),
	    ('legacy-boxlite-sandbox', 'sandbox', 'legacy-project', 'Existing BoxLite sandbox', '', TRUE,
	      '{"runtimeId":"legacy-boxlite-runtime","network":"restricted"}'::jsonb, NOW(), NOW())`); err != nil {
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
	if _, err := store.pool.Exec(ctx, `INSERT INTO worker_jobs
	  (id, server_id, resource_id, action, status, payload, created_at, updated_at)
	  VALUES
	    ($1, $5, 'legacy-docker-sandbox', 'create-sandbox', 'pending',
	      '{"driver":"docker","network":"restricted"}'::jsonb, NOW(), NOW()),
	    ($2, $5, 'legacy-microsandbox-sandbox', 'start-sandbox', 'pending',
	      '{"driver":"microsandbox","network":"restricted"}'::jsonb, NOW(), NOW()),
	    ($3, $5, 'legacy-boxlite-sandbox', 'restart-sandbox', 'pending',
	      '{"driver":"boxlite","network":"restricted"}'::jsonb, NOW(), NOW()),
	    ($4, $5, 'legacy-docker-sandbox', 'restart-sandbox', 'succeeded',
	      '{"driver":"docker","network":"restricted"}'::jsonb, NOW(), NOW())`,
		dockerJobID, microsandboxJobID, boxliteJobID, completedJobID, serverID); err != nil {
		t.Fatalf("insert v0.1.6 Worker jobs: %v", err)
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
	for _, expected := range []struct {
		id      string
		network string
	}{
		{id: "legacy-runtime", network: "egress"},
		{id: "legacy-microsandbox-runtime", network: "egress"},
		{id: "legacy-boxlite-runtime", network: "restricted"},
		{id: "legacy-docker-sandbox", network: "egress"},
		{id: "legacy-microsandbox-sandbox", network: "egress"},
		{id: "legacy-boxlite-sandbox", network: "restricted"},
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
