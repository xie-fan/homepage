// Package db opens the SQLite database and applies embedded schema
// migrations. Uses the pure-Go modernc.org/sqlite driver so the binary
// builds with CGO_ENABLED=0.
package db

import (
	"cmp"
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens (creating if needed) the SQLite database at path.
// journal_mode=WAL persists in the DB file; foreign_keys and busy_timeout
// are connection-scoped and set for every connection via DSN _pragma params.
func Open(path string) (*sql.DB, error) {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(on)")
	q.Add("_pragma", "busy_timeout(5000)")
	dsn := "file:" + path + "?" + q.Encode()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	// SQLite serializes writers anyway; a single connection turns
	// would-be SQLITE_BUSY errors into plain FIFO ordering.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect %q: %w", path, err)
	}
	return db, nil
}

// Migrate applies all pending migrations in numeric version order, each in
// its own transaction, and records them in schema_migrations. The
// schema_migrations table is created here (not in 0001) so it exists before
// any migration bookkeeping happens. Re-running is a no-op.
func Migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	migs, err := listMigrations()
	if err != nil {
		return err
	}
	for _, m := range migs {
		applied, err := isApplied(ctx, db, m.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := apply(ctx, db, m); err != nil {
			return err
		}
	}
	return nil
}

// migration is one embedded migration file paired with its version.
type migration struct {
	version int
	name    string
}

// listMigrations returns the embedded *.sql files sorted by version.
func listMigrations() ([]migration, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	return parseMigrations(names)
}

// parseMigrations validates NNNN_name.sql file names and returns them
// sorted by numeric prefix (0002 after 0001, 0010 after 0002).
func parseMigrations(names []string) ([]migration, error) {
	migs := make([]migration, 0, len(names))
	seen := map[int]bool{}
	for _, name := range names {
		v, err := migrationVersion(name)
		if err != nil {
			return nil, err
		}
		if seen[v] {
			return nil, fmt.Errorf("duplicate migration version %d (%s)", v, name)
		}
		seen[v] = true
		migs = append(migs, migration{version: v, name: name})
	}
	slices.SortFunc(migs, func(a, b migration) int {
		return cmp.Compare(a.version, b.version)
	})
	return migs, nil
}

// migrationVersion extracts the numeric prefix of a NNNN_name.sql file name.
func migrationVersion(name string) (int, error) {
	i := strings.IndexByte(name, '_')
	if i <= 0 || !strings.HasSuffix(name, ".sql") {
		return 0, fmt.Errorf("bad migration name %q: want NNNN_name.sql", name)
	}
	v, err := strconv.Atoi(name[:i])
	if err != nil {
		return 0, fmt.Errorf("bad migration name %q: %w", name, err)
	}
	return v, nil
}

func isApplied(ctx context.Context, db *sql.DB, version int) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check migration %d: %w", version, err)
	}
	return n > 0, nil
}

// apply runs one migration file inside a transaction and records its version.
func apply(ctx context.Context, db *sql.DB, m migration) error {
	body, err := migrationsFS.ReadFile("migrations/" + m.name)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", m.name, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", m.name, err)
	}
	defer tx.Rollback() // no-op once committed

	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("record migration %s: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.name, err)
	}
	return nil
}
