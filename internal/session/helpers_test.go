package session_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/brdsmth/beethoven/internal/agent"
	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/sandbox"
	"github.com/brdsmth/beethoven/internal/session"
)

func sandboxSpec(sessionID string) sandbox.Spec {
	return sandbox.Spec{Name: "beethoven-" + sessionID}
}

func newManager(t *testing.T, env *fake.Env) *session.Manager {
	m := session.New(session.Config{TurnTimeout: time.Minute, IdleTimeout: time.Hour, TokenKey: []byte("k")},
		env.DB, env.Box, agent.Claude{}, env.SCM, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { m.Shutdown(5 * time.Second) })
	return m
}
