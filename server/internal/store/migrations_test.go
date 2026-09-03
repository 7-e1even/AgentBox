package store

import (
	"context"
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
