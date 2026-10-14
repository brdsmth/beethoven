package fake

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/brdsmth/beethoven/internal/agent"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

// Env is a session manager wired to fakes, for tests.
type Env struct {
	M   *session.Manager
	DB  *store.Store
	Box *Provider
	SCM *SCM
}

func NewEnv(t testing.TB, mutate ...func(*session.Config)) *Env {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	cfg := session.Config{
		Image:       "test-image",
		IdleTimeout: time.Hour,
		TurnTimeout: time.Minute,
		ToolsURL:    "http://tools.test/mcp",
		TokenKey:    []byte("test-token-key"),
		Committer:   store.Author{Name: "Beethoven", Email: "beethoven@localhost"},
		Author:      store.Author{Name: "Test Owner", Email: "owner@example.com"},
	}
	for _, f := range mutate {
		f(&cfg)
	}
	box, scm := NewProvider(), &SCM{}
	m := session.New(cfg, db, box, agent.Claude{}, scm, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		m.Shutdown(5 * time.Second)
		db.Close()
	})
	return &Env{M: m, DB: db, Box: box, SCM: scm}
}

// WaitFor polls cond until it holds or the deadline passes.
func WaitFor(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
