package session_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

func argAfter(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

func environment(t *testing.T, env *fake.Env, e store.Environment) store.Environment {
	t.Helper()
	e.ID = store.NewID()
	if err := session.ValidateEnvironment(&e); err != nil {
		t.Fatal(err)
	}
	if err := env.DB.CreateEnvironment(ctx, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestEnvironmentSession(t *testing.T) {
	env := fake.NewEnv(t)
	env.DB.SaveSettings(ctx, store.Settings{Overrides: store.Overrides{Model: "sonnet", Instructions: "Be terse."}})
	e := environment(t, env, store.Environment{
		Name: "Web stack", Description: "API and frontend",
		Repos:       []store.RepoRef{{Repo: "acme/api", BaseBranch: "main"}, {Repo: "acme/web"}},
		SetupScript: "make deps", StartScript: "make up", InheritSecrets: true,
		Overrides: store.Overrides{Model: "opus", Effort: "high", Instructions: "Use pnpm."},
	})
	env.DB.SetSecret(ctx, "", "GLOBAL", "g")
	env.DB.SetSecret(ctx, store.EnvScope(e.ID), "ENV", "e")
	env.DB.SetSecret(ctx, "acme/api", "REPO", "r") // repository secrets stop at the environment boundary

	s, p := submit(t, env, "build it", func(r *session.Request) { r.EnvironmentID = e.ID })
	waitStatus(t, env, p.ID, store.Done)
	if !slices.Equal(store.RepoNames(s.Repos), []string{"acme/api", "acme/web"}) || s.EnvironmentID != e.ID {
		t.Fatalf("session = %+v", s)
	}

	boot := env.Box.Boots[0]
	if want := "api file:///remote/acme/api.git main\nweb file:///remote/acme/web.git -"; boot.Env["REPOS"] != want {
		t.Errorf("REPOS = %q", boot.Env["REPOS"])
	}
	if boot.Env["WORKDIR"] != "/workspace" || boot.Env["ENV_SETUP"] != "make deps" || boot.Env["ENV_START"] != "make up" || boot.Env["ENV_SETUP_ID"] == "" {
		t.Errorf("boot env = %v", boot.Env)
	}

	turn := env.Box.Turns[0]
	if argAfter(turn.Args, "--model") != "opus" || argAfter(turn.Args, "--effort") != "high" || turn.Dir != "/workspace" {
		t.Errorf("agent args = %v in %s", turn.Args, turn.Dir)
	}
	system := argAfter(turn.Args, "--append-system-prompt")
	for _, want := range []string{`"Web stack" environment: API and frontend`, "/workspace/api: acme/api (primary)", "Be terse.\n\nUse pnpm."} {
		if !strings.Contains(system, want) {
			t.Errorf("system prompt lacks %q:\n%s", want, system)
		}
	}
	if turn.Env["GLOBAL"] != "g" || turn.Env["ENV"] != "e" || turn.Env["REPO"] != "" {
		t.Errorf("secrets = GLOBAL=%q ENV=%q REPO=%q", turn.Env["GLOBAL"], turn.Env["ENV"], turn.Env["REPO"])
	}

	// Editing the environment does not move the session; inheritance and
	// credentials are read live.
	e.Repos = []store.RepoRef{{Repo: "acme/other"}}
	e.InheritSecrets = false
	env.DB.SaveEnvironment(ctx, &e)
	env.DB.SetCredential(ctx, "ANTHROPIC_API_KEY", "sk-cred")
	p2, _ := env.M.Enqueue(ctx, s.ID, session.PromptInput{Content: "again", Model: "haiku", Effort: "low", Source: "test"})
	waitStatus(t, env, p2.ID, store.Done)
	turn = env.Box.Turns[1]
	if got, _ := env.M.Session(ctx, s.ID); len(got.Repos) != 2 {
		t.Errorf("session repos changed with the environment: %+v", got.Repos)
	}
	if turn.Env["GLOBAL"] != "" || turn.Env["ENV"] != "e" || turn.Env["ANTHROPIC_API_KEY"] != "sk-cred" {
		t.Errorf("after disabling inheritance: GLOBAL=%q ENV=%q key=%q", turn.Env["GLOBAL"], turn.Env["ENV"], turn.Env["ANTHROPIC_API_KEY"])
	}
	if argAfter(turn.Args, "--model") != "haiku" || argAfter(turn.Args, "--effort") != "low" {
		t.Errorf("per-prompt model and effort ignored: %v", turn.Args)
	}
}

func TestSettingsShapeNewSessions(t *testing.T) {
	env := fake.NewEnv(t)
	env.DB.SaveSettings(ctx, store.Settings{BranchPrefix: "agents/", Overrides: store.Overrides{Image: "custom:1", CPUs: 2, MemoryMB: 4096}})
	s, p := submit(t, env, "x", func(r *session.Request) { r.Effort = "max" })
	waitStatus(t, env, p.ID, store.Done)
	if !strings.HasPrefix(s.Branch, "agents/") {
		t.Errorf("branch = %q", s.Branch)
	}
	if spec := env.Box.Specs[0]; spec.Image != "custom:1" || spec.CPUs != 2 || spec.MemoryMB != 4096 {
		t.Errorf("sandbox spec = %+v", spec)
	}
	if argAfter(env.Box.Turns[0].Args, "--effort") != "max" {
		t.Errorf("session effort ignored: %v", env.Box.Turns[0].Args)
	}
	if _, _, err := env.M.Submit(ctx, session.Request{Prompt: "x", Effort: "extreme", Source: "test"}); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("bad effort: err = %v", err)
	}
}

func TestValidateRepos(t *testing.T) {
	for _, bad := range [][]store.RepoRef{
		{{Repo: "no-slash"}},
		{{Repo: "a/b c"}},
		{{Repo: "acme/app", BaseBranch: "main; rm -rf /"}},
		{{Repo: "acme/app"}, {Repo: "other/app"}}, // same checkout directory
		{{Repo: "../etc"}},
	} {
		if _, err := session.ValidateRepos(bad); !errors.Is(err, session.ErrInvalid) {
			t.Errorf("ValidateRepos(%+v) err = %v", bad, err)
		}
	}
	got, err := session.ValidateRepos([]store.RepoRef{{Repo: " acme/app ", BaseBranch: "feature/x", PRURL: "dropped"}})
	if err != nil || got[0] != (store.RepoRef{Repo: "acme/app", BaseBranch: "feature/x"}) {
		t.Errorf("ValidateRepos = %+v, %v", got, err)
	}
}

func TestMultiRepoPullRequests(t *testing.T) {
	env := fake.NewEnv(t)
	s, p := submit(t, env, "x", func(r *session.Request) {
		r.Repos = []store.RepoRef{{Repo: "acme/api"}, {Repo: "acme/web", BaseBranch: "develop"}}
	})
	waitStatus(t, env, p.ID, store.Done)
	if _, err := env.M.OpenPR(ctx, s.ID, "", "t", "", nil); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("PR without naming a repository: err = %v", err)
	}
	if _, err := env.M.OpenPR(ctx, s.ID, "acme/elsewhere", "t", "", nil); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("PR for a foreign repository: err = %v", err)
	}
	draft := true
	url, err := env.M.OpenPR(ctx, s.ID, "acme/web", "t", "", &draft)
	if err != nil {
		t.Fatal(err)
	}
	if push := env.Box.Pushes[0]; push.Dir != "/workspace/web" {
		t.Errorf("pushed from %s", push.Dir)
	}
	if pr := env.SCM.PRs[0]; pr.Repo != "acme/web" || pr.Base != "develop" || !pr.Draft {
		t.Errorf("pr = %+v", pr)
	}
	got, _ := env.M.Session(ctx, s.ID)
	if got.Repos[0].PRURL != "" || got.Repos[1].PRURL != url {
		t.Errorf("repos = %+v", got.Repos)
	}
}

func TestMCPServersReachTheAgent(t *testing.T) {
	env := fake.NewEnv(t)
	e := environment(t, env, store.Environment{Name: "E", Repos: []store.RepoRef{{Repo: "acme/api"}}, InheritSecrets: true})
	for _, m := range []store.MCPServer{
		{ID: "1", Name: "sentry", Type: "http", URL: "https://mcp.sentry.dev/mcp", Secret: map[string]string{"Authorization": "Bearer s"}, Enabled: true},
		{ID: "2", Name: "db", Type: "stdio", Command: []string{"npx", "-y", "db-mcp"}, Secret: map[string]string{"DB_URL": "pg://x"}, Scope: store.EnvScope(e.ID), Enabled: true},
		{ID: "3", Name: "elsewhere", Type: "http", URL: "https://x", Scope: "acme/other", Enabled: true},
	} {
		m := m
		if err := env.DB.CreateMCPServer(ctx, &m); err != nil {
			t.Fatal(err)
		}
	}
	_, p := submit(t, env, "x", func(r *session.Request) { r.EnvironmentID = e.ID })
	waitStatus(t, env, p.ID, store.Done)

	var cfg struct {
		MCPServers map[string]struct {
			Type, URL, Command string
			Args               []string
			Headers, Env       map[string]string
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(argAfter(env.Box.Turns[0].Args, "--mcp-config")), &cfg); err != nil {
		t.Fatal(err)
	}
	servers := cfg.MCPServers
	if len(servers) != 3 || servers["beethoven"].Type != "http" {
		t.Fatalf("servers = %+v", servers)
	}
	if s := servers["sentry"]; s.Headers["Authorization"] != "Bearer s" {
		t.Errorf("sentry = %+v", s)
	}
	if s := servers["db"]; s.Type != "stdio" || s.Command != "npx" || !slices.Equal(s.Args, []string{"-y", "db-mcp"}) || s.Env["DB_URL"] != "pg://x" {
		t.Errorf("db = %+v", s)
	}
}

func TestChildInheritsWorkspace(t *testing.T) {
	env := fake.NewEnv(t)
	e := environment(t, env, store.Environment{Name: "E", Repos: []store.RepoRef{{Repo: "acme/api"}, {Repo: "acme/web"}}, InheritSecrets: true})
	parent, _ := submit(t, env, "parent", func(r *session.Request) { r.EnvironmentID = e.ID; r.Effort = "high" })
	child, _ := submit(t, env, "child", func(r *session.Request) { r.ParentID = parent.ID })
	if child.EnvironmentID != e.ID || len(child.Repos) != 2 || child.Effort != "high" {
		t.Errorf("child = %+v", child)
	}
	solo, _ := submit(t, env, "solo", func(r *session.Request) { r.ParentID = parent.ID; r.Repos = []store.RepoRef{{Repo: "acme/web"}} })
	if solo.EnvironmentID != "" || len(solo.Repos) != 1 {
		t.Errorf("child on one repository = %+v", solo)
	}
}
