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

	"github.com/jackc/pgx/v5"
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migrate: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(0x4147424f58)); err != nil {
		return fmt.Errorf("lock migrate: %w", err)
	}
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
	return nil
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
