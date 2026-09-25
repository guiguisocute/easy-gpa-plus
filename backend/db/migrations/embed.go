// Package migrations applies the checked-in SQL files without a second CLI.
package migrations

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.up.sql *.down.sql
var files embed.FS

const migrationLock int64 = 443297182771

func OpenPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	// Migration files contain multiple explicit statements and never parameters.
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return pgxpool.NewWithConfig(ctx, cfg)
}

func Up(ctx context.Context, pool *pgxpool.Pool) error {
	return up(ctx, pool, files)
}

func up(ctx context.Context, pool *pgxpool.Pool, source fs.FS) error {
	migrations, err := migrationFiles(source)
	if err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLock); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLock) }()
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migration (
		version BIGINT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}

	for _, migration := range migrations {
		name, version := migration.upFile, migration.version
		var applied bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migration WHERE version=$1)", version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		script, err := fs.ReadFile(source, name)
		if err != nil {
			return err
		}
		tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(script)); err == nil {
			_, err = tx.Exec(ctx, "INSERT INTO schema_migration (version) VALUES ($1)", version)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}

func DownOne(ctx context.Context, pool *pgxpool.Pool) error {
	return downOne(ctx, pool, files)
}

func downOne(ctx context.Context, pool *pgxpool.Pool, source fs.FS) error {
	migrations, err := migrationFiles(source)
	if err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLock); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLock) }()
	var version int64
	if err := conn.QueryRow(ctx, "SELECT version FROM schema_migration ORDER BY version DESC LIMIT 1").Scan(&version); err != nil {
		return err
	}
	var downFile string
	for _, migration := range migrations {
		if migration.version == version {
			downFile = migration.downFile
			break
		}
	}
	if downFile == "" {
		return fmt.Errorf("down migration for version %d not found", version)
	}
	script, err := fs.ReadFile(source, downFile)
	if err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, string(script)); err == nil {
		_, err = tx.Exec(ctx, "DELETE FROM schema_migration WHERE version=$1", version)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("revert migration %s: %w", downFile, err)
	}
	return tx.Commit(ctx)
}

type migration struct {
	version  int64
	upFile   string
	downFile string
}

// Validate the entire catalog before acquiring a connection or executing SQL.
// schema_migration only records numeric versions, so duplicate versions must
// never be silently skipped or paired with an unrelated rollback file.
func migrationFiles(source fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		return nil, fmt.Errorf("read migration files: %w", err)
	}
	byVersion := make(map[int64]migration)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		isUp := strings.HasSuffix(name, ".up.sql")
		if !isUp && !strings.HasSuffix(name, ".down.sql") {
			continue
		}
		version, err := versionOf(name)
		if err != nil {
			return nil, err
		}
		item := byVersion[version]
		item.version = version
		if isUp {
			if item.upFile != "" {
				return nil, fmt.Errorf("duplicate migration version %d: up files %q and %q", version, item.upFile, name)
			}
			item.upFile = name
		} else {
			if item.downFile != "" {
				return nil, fmt.Errorf("duplicate migration version %d: down files %q and %q", version, item.downFile, name)
			}
			item.downFile = name
		}
		byVersion[version] = item
	}
	migrations := make([]migration, 0, len(byVersion))
	for _, item := range byVersion {
		migrations = append(migrations, item)
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	for _, item := range migrations {
		if item.upFile == "" {
			return nil, fmt.Errorf("migration version %d: %q has no matching up file", item.version, item.downFile)
		}
		if item.downFile == "" {
			return nil, fmt.Errorf("migration version %d: %q has no matching down file", item.version, item.upFile)
		}
		if strings.TrimSuffix(item.upFile, ".up.sql") != strings.TrimSuffix(item.downFile, ".down.sql") {
			return nil, fmt.Errorf("migration version %d has mismatched up/down files: %q and %q", item.version, item.upFile, item.downFile)
		}
	}
	return migrations, nil
}

func versionOf(name string) (int64, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("migration filename %q has no numeric prefix", name)
	}
	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("migration filename %q: %w", name, err)
	}
	return version, nil
}
