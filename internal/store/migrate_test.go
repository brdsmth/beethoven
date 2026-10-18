package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func rawDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "m.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db, path
}

var files = fstest.MapFS{
	"m/001_a.sql": {Data: []byte(`CREATE TABLE a (x INTEGER);`)},
	"m/002_b.sql": {Data: []byte(`CREATE TABLE b (x INTEGER);`)},
	"m/003_c.sql": {Data: []byte(`CREATE TABLE c (x INTEGER);`)},
}

func names(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM schema_migrations ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		out = append(out, n)
	}
	return out
}

func TestMigrationsAreRecordedByName(t *testing.T) {
	db, path := rawDB(t)
	var seen []string
	backup := filepath.Join(filepath.Dir(path), "before.db")
	if err := Migrate(db, files, "m", func(pending []string, copy func(string) error) error {
		seen = pending
		return copy(backup)
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, []string{"001_a.sql", "002_b.sql", "003_c.sql"}) {
		t.Errorf("pending = %v", seen)
	}
	if got := names(t, db); len(got) != 3 {
		t.Errorf("recorded %v", got)
	}
	var version int
	db.QueryRow(`PRAGMA user_version`).Scan(&version)
	if version != 3 {
		t.Errorf("user_version = %d", version)
	}

	// Nothing pending: the hook isn't called.
	called := false
	Migrate(db, files, "m", func([]string, func(string) error) error { called = true; return nil })
	if called {
		t.Error("the hook ran with nothing pending")
	}
	// The backup is a database of its own.
	b, _ := sql.Open("sqlite", backup)
	defer b.Close()
	if err := b.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(new(int)); err != nil {
		t.Errorf("backup: %v", err)
	}
}

// TestCountedDatabasesKeepTheirMigrations upgrades a database from before
// names were recorded: its user_version said how many had run.
func TestCountedDatabasesKeepTheirMigrations(t *testing.T) {
	db, _ := rawDB(t)
	db.Exec(`CREATE TABLE a (x INTEGER); CREATE TABLE b (x INTEGER)`)
	db.Exec(`PRAGMA user_version = 2`)
	var pending []string
	if err := Migrate(db, files, "m", func(p []string, _ func(string) error) error { pending = p; return nil }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pending, []string{"003_c.sql"}) {
		t.Errorf("pending = %v; the first two had run", pending)
	}
	if got := names(t, db); !slices.Equal(got, []string{"001_a.sql", "002_b.sql", "003_c.sql"}) {
		t.Errorf("recorded %v", got)
	}
}

// TestAMigrationFromAnotherBranchStillRuns is the case counting got wrong:
// a migration that sorts before one already applied.
func TestAMigrationFromAnotherBranchStillRuns(t *testing.T) {
	db, _ := rawDB(t)
	only := fstest.MapFS{"m/001_a.sql": files["m/001_a.sql"], "m/003_c.sql": files["m/003_c.sql"]}
	if err := Migrate(db, only, "m", nil); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db, files, "m", nil); err != nil { // 002 merged later
		t.Fatal(err)
	}
	if got := names(t, db); len(got) != 3 {
		t.Errorf("recorded %v; 002 was skipped", got)
	}
	if err := db.QueryRow(`SELECT count(*) FROM b`).Scan(new(int)); err != nil {
		t.Errorf("002 didn't run: %v", err)
	}
}

func TestAFailedMigrationKeepsTheOnesBefore(t *testing.T) {
	db, _ := rawDB(t)
	bad := fstest.MapFS{"m/001_a.sql": files["m/001_a.sql"], "m/002_bad.sql": {Data: []byte(`CREATE TABLE a (x INTEGER);`)}}
	err := Migrate(db, bad, "m", nil)
	if err == nil || !strings.Contains(err.Error(), "002_bad.sql") {
		t.Fatalf("err = %v", err)
	}
	if got := names(t, db); !slices.Equal(got, []string{"001_a.sql"}) {
		t.Errorf("recorded %v", got)
	}

	// A hook that refuses leaves the database untouched.
	db2, _ := rawDB(t)
	stop := errors.New("no backup")
	if err := Migrate(db2, files, "m", func([]string, func(string) error) error { return stop }); !errors.Is(err, stop) {
		t.Errorf("err = %v", err)
	}
	var tables int
	db2.QueryRow(`SELECT count(*) FROM sqlite_master WHERE tbl_name != 'schema_migrations'`).Scan(&tables)
	if tables != 0 {
		t.Errorf("%d tables after a refused migration", tables)
	}
	if err := Migrate(db2, files, "m", nil); err != nil { // and isn't mistaken for someone else's
		t.Errorf("after a refused migration: %v", err)
	}
}
