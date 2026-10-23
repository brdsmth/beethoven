package store

import (
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
	"testing/fstest"
)

// countedDB builds a database the way builds from before names were
// recorded did: the given migrations run in order, user_version counting
// them.
func countedDB(t *testing.T, names []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "counted.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)
	for _, name := range names {
		body, _ := migrations.ReadFile("migrations/" + name)
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := raw.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, len(names))); err != nil {
		t.Fatal(err)
	}
	return path
}

// through015 is every migration up to the two numbered 016.
func through015(t *testing.T) []string {
	t.Helper()
	paths, _ := fs.Glob(migrations, "migrations/*.sql")
	sort.Strings(paths)
	var out []string
	for _, p := range paths {
		if name := filepath.Base(p); name < "016" {
			out = append(out, name)
		}
	}
	return out
}

func requireTables(t *testing.T, s *Store, tables ...string) {
	t.Helper()
	for _, table := range tables {
		var n int
		s.r.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n)
		if n == 0 {
			t.Errorf("table %s is missing", table)
		}
	}
}

// TestCountedDatabaseWithPersonalTeamsSixteenth is production as deployed:
// counted to 16 with personal teams, and so without links.
func TestCountedDatabaseWithPersonalTeamsSixteenth(t *testing.T) {
	path := countedDB(t, append(through015(t), "016_personal_teams.sql"))
	s, err := Open(path, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	requireTables(t, s, "session_shares", "session_links", "inbox", "outbound")
}

// TestCountedDatabaseWithLinksSixteenth is the other branch's database.
func TestCountedDatabaseWithLinksSixteenth(t *testing.T) {
	path := countedDB(t, append(through015(t), "016_links.sql"))
	s, err := Open(path, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	requireTables(t, s, "session_shares", "session_links")
}

func TestCountedDatabaseWithBothSixteenths(t *testing.T) {
	path := countedDB(t, append(through015(t), "016_links.sql", "016_personal_teams.sql"))
	s, err := Open(path, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	s.r.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n)
	if paths, _ := fs.Glob(migrations, "migrations/*.sql"); n != len(paths) {
		t.Errorf("recorded %d migrations, want %d", n, len(paths))
	}
}

// TestAMigrationFromAnotherBranchStillRuns is the case counting got wrong:
// a migration that sorts before one already applied.
func TestAMigrationFromAnotherBranchStillRuns(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	a := &fstest.MapFile{Data: []byte(`CREATE TABLE a (x INTEGER);`)}
	b := &fstest.MapFile{Data: []byte(`CREATE TABLE b (x INTEGER);`)}
	c := &fstest.MapFile{Data: []byte(`CREATE TABLE c (x INTEGER);`)}
	if err := migrateFrom(db, fstest.MapFS{"m/001_a.sql": a, "m/003_c.sql": c}, "m"); err != nil {
		t.Fatal(err)
	}
	if err := migrateFrom(db, fstest.MapFS{"m/001_a.sql": a, "m/002_b.sql": b, "m/003_c.sql": c}, "m"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM b`).Scan(new(int)); err != nil {
		t.Errorf("002 didn't run: %v", err)
	}
	var version int
	db.QueryRow(`PRAGMA user_version`).Scan(&version)
	if version != 3 {
		t.Errorf("user_version = %d, want 3", version)
	}
}
