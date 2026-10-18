package session_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

func TestShowFile(t *testing.T) {
	env := fake.NewEnv(t)
	s, p := submit(t, env, "make a mockup", func(r *session.Request) { r.Repos = []store.RepoRef{{Repo: "acme/app"}} })
	waitStatus(t, env, p.ID, store.Done)
	dir := session.Workdir(s)
	env.Box.PutFile(dir+"/mockups/home.html", []byte("<h1>Home</h1>"))

	a, err := env.M.ShowFile(ctx, s.ID, "mockups/home.html", "Home page")
	if err != nil {
		t.Fatal(err)
	}
	if a.Path != dir+"/mockups/home.html" || a.Name != "home.html" || a.ContentType != "text/html; charset=utf-8" || a.Size != 13 {
		t.Errorf("artifact = %+v", a)
	}
	got, err := env.DB.Artifact(ctx, s.ID, a.ID)
	if err != nil || string(got.Data) != "<h1>Home</h1>" || got.Title != "Home page" {
		t.Errorf("stored = %+v, %v", got, err)
	}
	evs, _ := env.DB.Events(ctx, s.ID, 0, 1000)
	var shown map[string]any
	for _, e := range evs {
		if e.Type == session.EvArtifact {
			json.Unmarshal(e.Data, &shown)
		}
	}
	if shown["kind"] != "file" || shown["id"] != a.ID {
		t.Errorf("artifact event = %v", shown)
	}

	// Showing it again keeps both versions.
	env.Box.PutFile(dir+"/mockups/home.html", []byte("<h1>Home v2</h1>"))
	if _, err := env.M.ShowFile(ctx, s.ID, dir+"/mockups/home.html", ""); err != nil {
		t.Fatal(err)
	}
	if list, _ := env.M.Artifacts(ctx, s.ID); len(list) != 2 || list[0].ID != a.ID {
		t.Errorf("artifacts = %+v", list)
	}
	if _, err := env.M.ShowFile(ctx, s.ID, "missing.png", ""); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("missing file: %v", err)
	}
}

func TestContentType(t *testing.T) {
	for name, want := range map[string]string{
		"a.png": "image/png", "notes.md": "text/markdown; charset=utf-8", "main.go": "text/plain; charset=utf-8",
		"page.html": "text/html; charset=utf-8", "logo.svg": "image/svg+xml", "data.csv": "text/csv; charset=utf-8",
		"report.pdf": "application/pdf", "Makefile": "text/plain; charset=utf-8",
	} {
		if got := session.ContentType(name, []byte("all: build\n")); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	if got := session.ContentType("blob", []byte{0, 1, 2, 0}); got != "application/octet-stream" {
		t.Errorf("binary: %q", got)
	}
}
