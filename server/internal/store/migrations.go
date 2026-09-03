package store

import (
	"cmp"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// schema.sql is the one-time compatibility baseline for databases created by
// versions that executed the complete schema on every startup. New changes
// belong in migrations/*.sql; changing an applied migration is rejected by its
// checksum.
//
//go:embed schema.sql migrations/*.sql
var migrationFiles embed.FS

// schema is kept as the baseline SQL view used by focused schema regression
// tests. Runtime migration execution is ledger-driven through migrationFiles.
//
//go:embed schema.sql
var schema string

type databaseMigration struct {
	id       string
	contents string
	checksum string
}

const (
	databaseMigrationLockKey           int64 = 0x4147424f58
	databaseMigrationLockRetryInterval       = 100 * time.Millisecond
)

type concurrentIndexSpec struct {
	name      string
	table     string
	keys      []concurrentIndexKeySpec
	predicate string
}

type concurrentIndexKeySpec struct {
	definition string
	descending bool
	nullsFirst bool
}

var systemLogRetentionIndexes = []concurrentIndexSpec{
	{
		name:  "idx_system_logs_best_effort_retention_v1",
		table: "system_logs",
		keys: []concurrentIndexKeySpec{
			{definition: "created_at", descending: true, nullsFirst: true},
			{definition: "id", descending: true, nullsFirst: true},
		},
		predicate: "COALESCE(detail->>'delivery', 'best-effort') <> 'transactional'",
	},
	{
		name:  "idx_system_logs_transactional_retention_v1",
		table: "system_logs",
		keys: []concurrentIndexKeySpec{
			{definition: "created_at", descending: true, nullsFirst: true},
			{definition: "id", descending: true, nullsFirst: true},
		},
		predicate: "detail->>'delivery' = 'transactional'",
	},
}

func loadDatabaseMigrations() ([]databaseMigration, error) {
	baseline, err := migrationFiles.ReadFile("schema.sql")
	if err != nil {
		return nil, fmt.Errorf("read baseline migration: %w", err)
	}
	migrations := []databaseMigration{newDatabaseMigration("0001_legacy_schema", string(baseline))}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("list database migrations: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".sql" {
			continue
		}
		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read database migration %s: %w", entry.Name(), err)
		}
		migrations = append(migrations, newDatabaseMigration(strings.TrimSuffix(entry.Name(), ".sql"), string(contents)))
	}
	slices.SortFunc(migrations, func(a, b databaseMigration) int { return cmp.Compare(a.id, b.id) })
	for i := 1; i < len(migrations); i++ {
		if migrations[i-1].id == migrations[i].id {
			return nil, fmt.Errorf("duplicate database migration %q", migrations[i].id)
		}
	}
	return migrations, nil
}

func newDatabaseMigration(id, contents string) databaseMigration {
	sum := sha256.Sum256([]byte(contents))
	return databaseMigration{id: id, contents: contents, checksum: hex.EncodeToString(sum[:])}
}

func (s *Store) migrate(ctx context.Context) error {
	migrations, err := loadDatabaseMigrations()
	if err != nil {
		return err
	}
	return s.withDatabaseMigrationLock(ctx, func(conn *pgxpool.Conn) error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migrate: %w", err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
			id TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			checksum TEXT
		); ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum TEXT`); err != nil {
			return fmt.Errorf("prepare migration ledger: %w", err)
		}
		for _, migration := range migrations {
			if err := applyDatabaseMigration(ctx, tx, migration); err != nil {
				return err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migrate: %w", err)
		}
		return ensureSystemLogRetentionIndexes(ctx, conn)
	})
}

// withDatabaseMigrationLock serializes schema and seed initialization on one
// dedicated connection. Contenders poll outside a transaction: a blocking
// advisory-lock query inside a transaction can retain an old snapshot that a
// CREATE INDEX CONCURRENTLY holder must wait for, producing a lock cycle.
func (s *Store) withDatabaseMigrationLock(
	ctx context.Context,
	operation func(*pgxpool.Conn) error,
) (resultErr error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	if err := waitForDatabaseMigrationLock(ctx, conn); err != nil {
		// The server may have granted the session lock just before a canceled
		// query became unobservable to the client. Close the physical session
		// instead of returning a possibly lock-bearing connection to the pool.
		discardDatabaseMigrationConnection(conn)
		return err
	}
	defer func() {
		if err := releaseDatabaseMigrationLock(conn); err != nil && resultErr == nil {
			resultErr = err
		}
	}()
	return operation(conn)
}

func waitForDatabaseMigrationLock(ctx context.Context, conn *pgxpool.Conn) error {
	ticker := time.NewTicker(databaseMigrationLockRetryInterval)
	defer ticker.Stop()
	for {
		var locked bool
		if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", databaseMigrationLockKey).Scan(&locked); err != nil {
			return fmt.Errorf("try lock database migrations: %w", err)
		}
		if locked {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for database migration lock: %w", context.Cause(ctx))
		case <-ticker.C:
		}
	}
}

func releaseDatabaseMigrationLock(conn *pgxpool.Conn) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var unlocked bool
	if err := conn.QueryRow(cleanupCtx, "SELECT pg_advisory_unlock($1)", databaseMigrationLockKey).Scan(&unlocked); err != nil {
		discardDatabaseMigrationConnection(conn)
		return fmt.Errorf("unlock database migrations: %w", err)
	}
	if !unlocked {
		discardDatabaseMigrationConnection(conn)
		return errors.New("unlock database migrations: lock was not held")
	}
	conn.Release()
	return nil
}

func discardDatabaseMigrationConnection(conn *pgxpool.Conn) {
	// Hijack first so even an unresponsive close can never put this physical
	// session, and any advisory lock it may hold, back into the pool.
	rawConn := conn.Hijack()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = rawConn.Close(cleanupCtx)
}

func applyDatabaseMigration(ctx context.Context, tx pgx.Tx, migration databaseMigration) error {
	var storedChecksum *string
	err := tx.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE id = $1`, migration.id).Scan(&storedChecksum)
	if err == nil {
		if storedChecksum == nil || *storedChecksum != migration.checksum {
			return fmt.Errorf("database migration %s checksum does not match the applied migration", migration.id)
		}
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check database migration %s: %w", migration.id, err)
	}
	if _, err := tx.Exec(ctx, migration.contents); err != nil {
		return fmt.Errorf("apply database migration %s: %w", migration.id, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (id, checksum) VALUES ($1, $2)`, migration.id, migration.checksum); err != nil {
		return fmt.Errorf("record database migration %s: %w", migration.id, err)
	}
	return nil
}

// ensureSystemLogRetentionIndexes installs the large-table indexes outside an
// explicit transaction so PostgreSQL can build them without blocking writes.
// The caller holds the session-level migration lock on conn across the ledger
// transaction and these builds. The 0009 marker remains the durable schema
// version, and this invariant is checked on every startup so a crash is
// recoverable.
func ensureSystemLogRetentionIndexes(ctx context.Context, conn *pgxpool.Conn) error {
	var schemaName string
	if err := conn.QueryRow(ctx, "SELECT current_schema()").Scan(&schemaName); err != nil {
		return fmt.Errorf("load online migration schema: %w", err)
	}
	for _, spec := range systemLogRetentionIndexes {
		if err := ensureConcurrentIndex(ctx, conn, schemaName, spec); err != nil {
			return err
		}
	}
	return nil
}

type concurrentIndexState struct {
	valid          bool
	ready          bool
	live           bool
	tableName      string
	accessMethod   string
	unique         bool
	keyCount       int
	attributeCount int
	hasExpressions bool
	keyDefinitions []string
	keyDescending  []bool
	keyNullsFirst  []bool
	definition     string
	predicate      string
}

func ensureConcurrentIndex(ctx context.Context, conn interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}, schemaName string, spec concurrentIndexSpec) error {
	state, found, err := loadConcurrentIndexState(ctx, conn, schemaName, spec.name)
	if err != nil {
		return err
	}
	if found && state.tableName != spec.table {
		return fmt.Errorf("online index %s belongs to table %s, want %s", spec.name, state.tableName, spec.table)
	}
	if found && !state.valid {
		indexIdentifier := pgx.Identifier{schemaName, spec.name}.Sanitize()
		if _, err := conn.Exec(ctx, "DROP INDEX CONCURRENTLY "+indexIdentifier); err != nil {
			return fmt.Errorf("drop invalid online index %s: %w", spec.name, err)
		}
		found = false
	}
	if !found {
		indexIdentifier := pgx.Identifier{spec.name}.Sanitize()
		tableIdentifier := pgx.Identifier{schemaName, spec.table}.Sanitize()
		keyDefinitions := make([]string, 0, len(spec.keys))
		for _, key := range spec.keys {
			definition := key.definition
			if key.descending {
				definition += " DESC"
			} else {
				definition += " ASC"
			}
			if key.nullsFirst {
				definition += " NULLS FIRST"
			} else {
				definition += " NULLS LAST"
			}
			keyDefinitions = append(keyDefinitions, definition)
		}
		query := "CREATE INDEX CONCURRENTLY " + indexIdentifier + " ON " + tableIdentifier +
			"(" + strings.Join(keyDefinitions, ", ") + ") WHERE " + spec.predicate
		if _, err := conn.Exec(ctx, query); err != nil {
			return fmt.Errorf("create online index %s: %w", spec.name, err)
		}
		state, found, err = loadConcurrentIndexState(ctx, conn, schemaName, spec.name)
		if err != nil {
			return err
		}
	}
	if !found || !state.valid || !state.ready || !state.live {
		return fmt.Errorf("online index %s is not valid after creation", spec.name)
	}
	if state.accessMethod != "btree" || state.unique || state.hasExpressions ||
		state.keyCount != len(spec.keys) || state.attributeCount != len(spec.keys) ||
		len(state.keyDefinitions) != len(spec.keys) || len(state.keyDescending) != len(spec.keys) ||
		len(state.keyNullsFirst) != len(spec.keys) {
		return fmt.Errorf("online index %s has unexpected index properties", spec.name)
	}
	for index, expected := range spec.keys {
		if normalizeIndexSQL(state.keyDefinitions[index]) != normalizeIndexSQL(expected.definition) {
			return fmt.Errorf("online index %s has unexpected key definition", spec.name)
		}
		if state.keyDescending[index] != expected.descending || state.keyNullsFirst[index] != expected.nullsFirst {
			return fmt.Errorf("online index %s has unexpected key ordering", spec.name)
		}
	}
	if normalizeIndexPredicate(state.predicate) != normalizeIndexPredicate(spec.predicate) {
		return fmt.Errorf("online index %s has unexpected predicate", spec.name)
	}
	return nil
}

func normalizeIndexSQL(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

func normalizeIndexPredicate(value string) string {
	var normalized strings.Builder
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '\'' {
			normalized.WriteByte(character)
			for index++; index < len(value); index++ {
				normalized.WriteByte(value[index])
				if value[index] != '\'' {
					continue
				}
				if index+1 < len(value) && value[index+1] == '\'' {
					index++
					normalized.WriteByte(value[index])
					continue
				}
				break
			}
			continue
		}
		if character == ' ' || character == '\t' || character == '\r' || character == '\n' {
			continue
		}
		if index+6 <= len(value) && strings.EqualFold(value[index:index+6], "::text") {
			index += 5
			continue
		}
		if character >= 'A' && character <= 'Z' {
			character += 'a' - 'A'
		}
		normalized.WriteByte(character)
	}
	value = normalized.String()
	for hasSingleOuterParentheses(value) {
		value = value[1 : len(value)-1]
	}
	// PostgreSQL deparses the fixed JSON text operand with one grouping pair.
	// Remove only that known redundant pair; all predicate operators and other
	// grouping remain part of the whole-string equality check.
	return strings.ReplaceAll(value, "(detail->>'delivery')", "detail->>'delivery'")
}

func hasSingleOuterParentheses(value string) bool {
	if len(value) < 2 || value[0] != '(' || value[len(value)-1] != ')' {
		return false
	}
	depth := 0
	for index, character := range value {
		switch character {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && index != len(value)-1 {
				return false
			}
		}
		if depth < 0 {
			return false
		}
	}
	return depth == 0
}

type concurrentIndexQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadConcurrentIndexState(
	ctx context.Context,
	query concurrentIndexQuerier,
	schemaName string,
	indexName string,
) (concurrentIndexState, bool, error) {
	var state concurrentIndexState
	err := query.QueryRow(ctx, `SELECT indexed.relname, index.indisvalid,
		index.indisready, index.indislive, method.amname, index.indisunique,
		index.indnkeyatts::integer, index.indnatts::integer, index.indexprs IS NOT NULL,
		ARRAY(SELECT pg_get_indexdef(index.indexrelid, position, TRUE)
		  FROM generate_series(1, index.indnkeyatts::integer) position ORDER BY position),
		ARRAY(SELECT COALESCE(pg_index_column_has_property(index.indexrelid, position, 'desc'), FALSE)
		  FROM generate_series(1, index.indnkeyatts::integer) position ORDER BY position),
		ARRAY(SELECT COALESCE(pg_index_column_has_property(index.indexrelid, position, 'nulls_first'), FALSE)
		  FROM generate_series(1, index.indnkeyatts::integer) position ORDER BY position),
		pg_get_indexdef(index.indexrelid), COALESCE(pg_get_expr(index.indpred, index.indrelid), '')
		FROM pg_class named
		JOIN pg_namespace namespace ON namespace.oid = named.relnamespace
		JOIN pg_index index ON index.indexrelid = named.oid
		JOIN pg_class indexed ON indexed.oid = index.indrelid
		JOIN pg_am method ON method.oid = named.relam
		WHERE namespace.nspname = $1 AND named.relname = $2`, schemaName, indexName).Scan(
		&state.tableName, &state.valid, &state.ready, &state.live,
		&state.accessMethod, &state.unique, &state.keyCount, &state.attributeCount, &state.hasExpressions,
		&state.keyDefinitions, &state.keyDescending, &state.keyNullsFirst, &state.definition, &state.predicate,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return concurrentIndexState{}, false, nil
	}
	if err != nil {
		return concurrentIndexState{}, false, fmt.Errorf("inspect online index %s: %w", indexName, err)
	}
	return state, true, nil
}
