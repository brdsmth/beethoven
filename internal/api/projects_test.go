package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brdsmth/beethoven/internal/api"
	"github.com/brdsmth/beethoven/internal/automation"
	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/github"
	"github.com/brdsmth/beethoven/internal/store"
)

func TestStartProject(t *testing.T) {
	ctx := context.Background()
	gh := fake.NewGitHub(t)
	gh.SetAccount(2, "acme", "Organization", map[string]string{"contents": "write", "repository_creation": "write"})
	env := fake.NewEnv(t)
	client := github.New("")
	client.API, client.Web, client.DB = gh.URL, gh.URL, env.DB
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer((&api.Server{M: env.M, Auto: automation.New(env.M, log), GitHub: client, Log: log}).Handler())
	t.Cleanup(srv.Close)
	if _, err := client.RegisterApp(ctx, "manifest-code"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Connect(ctx, "oauth-code", "http://beethoven.test/github/authorized"); err != nil {
		t.Fatal(err)
	}

	code, out := do(t, "GET", srv.URL+"/api/projects/options", "")
	if owners, _ := out["owners"].([]any); code != 200 || len(owners) != 1 || owners[0].(map[string]any)["login"] != "acme" {
		t.Fatalf("options: %d %v", code, out)
	}
	if stacks, _ := out["stacks"].([]any); len(stacks) < 2 {
		t.Errorf("stacks = %v", out["stacks"])
	}

	// Nothing is created until the whole request checks out.
	for body, want := range map[string]string{
		`{"owner":"acme","name":"bad name"}`:                        "repository name",
		`{"owner":"acme","name":"x","stack":"cobol"}`:               "unknown stack",
		`{"owner":"acme","name":"x","variables":"API_KEY=1\nnope"}`: "line 2",
		`{"owner":"acme","name":"x","variables":"HOME=/tmp"}`:       "set by the server",
		`{"owner":"acme","name":"x","effort":"extreme"}`:            "effort",
		`{"owner":"","name":"x"}`:                                   "account",
	} {
		if code, out := do(t, "POST", srv.URL+"/api/projects", body); code != 400 || !strings.Contains(out["error"].(string), want) {
			t.Errorf("%s: %d %v, want 400 mentioning %q", body, code, out, want)
		}
	}
	if len(gh.Created) != 0 {
		t.Fatalf("created %v for refused requests", gh.Created)
	}

	code, out = do(t, "POST", srv.URL+"/api/projects", `{"owner":"acme","name":"new-app","description":"A CLI",
		"stack":"go","variables":"API_KEY=abc\nexport DEBUG=1","prompt":"Build the CLI"}`)
	if code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	if got := gh.Created[0]; got["name"] != "new-app" || got["private"] != true || got["gitignore_template"] != "Go" {
		t.Errorf("asked GitHub for %v", got)
	}
	e := out["environment"].(map[string]any)
	id := e["id"].(string)
	saved, err := env.DB.Environment(ctx, id)
	if err != nil || saved.Name != "new-app" || saved.Description != "A CLI" || len(saved.Repos) != 1 ||
		saved.Repos[0].Repo != "acme/new-app" || !strings.Contains(saved.SetupScript, "go.dev") || !saved.InheritSecrets {
		t.Errorf("environment = %+v, %v", saved, err)
	}
	secrets, _ := env.DB.Secrets(ctx, ptr(store.EnvScope(id)))
	if len(secrets) != 2 || secrets[0].Name != "API_KEY" || secrets[1].Name != "DEBUG" {
		t.Errorf("secrets = %+v", secrets)
	}
	sess, _ := out["session"].(map[string]any)
	if sess == nil || sess["environment_id"] != id || out["prompt"].(map[string]any)["content"] != "Build the CLI" {
		t.Errorf("first session: %v", out)
	}

	// A second project of the same name: the environment would be named
	// after the full repository, but GitHub has it already.
	code, out = do(t, "POST", srv.URL+"/api/projects", `{"owner":"acme","name":"new-app"}`)
	if code != 400 || !strings.Contains(out["error"].(string), "already exists on GitHub") {
		t.Errorf("same name again: %d %v", code, out)
	}
	code, out = do(t, "POST", srv.URL+"/api/projects", `{"owner":"acme","name":"quiet","public":true}`)
	if code != 201 || out["session"] != nil || gh.Created[1]["private"] != false {
		t.Errorf("without a prompt: %d %v; want no session", code, out)
	}
	if code, out := do(t, "POST", srv.URL+"/api/projects", `{"owner":"octo","name":"mine"}`); code != 502 {
		t.Errorf("where the app can't create repositories: %d %v", code, out)
	}
}

func ptr[T any](v T) *T { return &v }
