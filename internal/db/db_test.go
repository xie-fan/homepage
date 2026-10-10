package db

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

// openTest opens a fresh database in a temp dir and migrates it.
func openTest(t *testing.T) (context.Context, string) {
	t.Helper()
	return context.Background(), filepath.Join(t.TempDir(), "test.db")
}

func TestOpenSetsPragmas(t *testing.T) {
	ctx, path := openTest(t)
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var journal string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if journal != "wal" {
		t.Errorf("journal_mode = %q, want wal", journal)
	}
	var fk int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
	var timeout int
	if err := db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if timeout != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", timeout)
	}
}

func TestMigrateFreshDB(t *testing.T) {
	ctx, path := openTest(t)
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, table := range []string{"bookmarks", "settings", "schema_migrations"} {
		var name string
		err := db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
	var idx string
	if err := db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_bookmarks_position'`).Scan(&idx); err != nil {
		t.Errorf("index idx_bookmarks_position missing: %v", err)
	}

	// Version 1 recorded exactly once.
	var versions []int
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan version: %v", err)
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate versions: %v", err)
	}
	if !slices.Equal(versions, []int{1}) {
		t.Errorf("schema_migrations versions = %v, want [1]", versions)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	ctx, path := openTest(t)
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	// Second run must be a no-op: still exactly one applied version.
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if n != 1 {
		t.Errorf("schema_migrations rows = %d, want 1", n)
	}
	// Schema still usable after re-migration.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO bookmarks (title, url, position, created_at, updated_at)
		 VALUES ('x', 'https://example.com', 0, 'now', 'now')`); err != nil {
		t.Errorf("insert into bookmarks after re-migrate: %v", err)
	}
}

func TestParseMigrationsOrdering(t *testing.T) {
	migs, err := parseMigrations([]string{
		"0010_later.sql",
		"0002_second.sql",
		"0001_init.sql",
	})
	if err != nil {
		t.Fatalf("parseMigrations: %v", err)
	}
	got := make([]string, 0, len(migs))
	for _, m := range migs {
		got = append(got, m.name)
	}
	want := []string{"0001_init.sql", "0002_second.sql", "0010_later.sql"}
	if !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestParseMigrationsBadNames(t *testing.T) {
	for _, names := range [][]string{
		{"init.sql"},                 // missing numeric prefix
		{"0001.sql"},                 // missing _name part
		{"0001_a.sql", "0001_b.sql"}, // duplicate version
		{"abc_x.sql"},                // non-numeric prefix
	} {
		if _, err := parseMigrations(names); err == nil {
			t.Errorf("parseMigrations(%v) = nil error, want error", names)
		}
	}
}
