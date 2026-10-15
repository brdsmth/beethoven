package store

import (
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMigratesVersion1Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	v1, _ := migrations.ReadFile("migrations/001_init.sql")
	for _, q := range []string{string(v1), `PRAGMA user_version = 1`,
		`INSERT INTO sessions (id, title, repo, base_branch, branch, source, pr_url, created_at, updated_at)
		 VALUES ('s1', 't', 'acme/app', 'dev', 'beethoven/s1', 'web', 'https://github.com/acme/app/pull/7', 0, 0),
		        ('s2', 't', '', '', 'beethoven/s2', 'web', '', 0, 0)`,
		`INSERT INTO automations (id, name, repo, base_branch, prompt, hook_hash, created_at)
		 VALUES ('a1', 'n', 'acme/app', '', 'p', 'h', 0)`} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()

	s, err := Open(path, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, _ := s.Session(ctx, "s1")
	if want := []RepoRef{{Repo: "acme/app", BaseBranch: "dev", PRURL: "https://github.com/acme/app/pull/7"}}; !slices.Equal(got.Repos, want) {
		t.Errorf("migrated repos = %+v", got.Repos)
	}
	if got, _ := s.Session(ctx, "s2"); len(got.Repos) != 0 {
		t.Errorf("scratch session repos = %+v", got.Repos)
	}
	if a, _ := s.Automation(ctx, "a1"); len(a.Repos) != 1 || a.Repos[0].Repo != "acme/app" {
		t.Errorf("migrated automation = %+v", a)
	}
	var version int
	s.r.QueryRow(`PRAGMA user_version`).Scan(&version)
	if names, _ := fs.Glob(migrations, "migrations/*.sql"); version != len(names) {
		t.Errorf("user_version = %d, want %d", version, len(names))
	}
}

func TestSecretLayers(t *testing.T) {
	s := open(t)
	set := func(scope, name, value string) {
		if err := s.SetSecret(ctx, scope, name, value); err != nil {
			t.Fatal(err)
		}
	}
	set("", "A", "global")
	set("", "B", "global")
	set("acme/app", "A", "app")
	set("acme/lib", "A", "lib")
	set("acme/lib", "C", "lib")
	set(EnvScope("e1"), "A", "env")

	cases := []struct {
		name   string
		target SecretTarget
		want   map[string]string
	}{
		{"scratch", SecretTarget{}, map[string]string{"A": "global", "B": "global"}},
		{"repos, primary wins", SecretTarget{Repos: []string{"acme/app", "acme/lib"}}, map[string]string{"A": "app", "B": "global", "C": "lib"}},
		{"environment", SecretTarget{EnvironmentID: "e1", InheritGlobal: true, Repos: []string{"acme/lib"}}, map[string]string{"A": "env", "B": "global"}},
		{"environment without global", SecretTarget{EnvironmentID: "e1", Repos: []string{"acme/lib"}}, map[string]string{"A": "env"}},
	}
	for _, c := range cases {
		got, err := s.SecretEnv(ctx, c.target)
		if err != nil || len(got) != len(c.want) {
			t.Errorf("%s: env = %v, %v", c.name, got, err)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q", c.name, k, got[k], v)
			}
		}
	}

	eff, _ := s.EffectiveSecrets(ctx, SecretTarget{Repos: []string{"acme/app", "acme/lib"}})
	if len(eff) != 3 || eff[0].Name != "A" || eff[0].Scope != "acme/app" || !slices.Equal(eff[0].Overrides, []string{"", "acme/lib"}) {
		t.Errorf("effective = %+v", eff)
	}
}

func TestSecretRulesAndCredentials(t *testing.T) {
	s := open(t)
	for _, name := range []string{"PATH", "GH_TOKEN", "BEETHOVEN_X", "git_author_name", "1BAD", "has space"} {
		if err := s.SetSecret(ctx, "", name, "v"); !errors.Is(err, ErrInvalid) {
			t.Errorf("SetSecret(%q) err = %v", name, err)
		}
	}
	if err := s.SetSecret(ctx, "", "BIG", strings.Repeat("x", MaxSecretBytes+1)); !errors.Is(err, ErrInvalid) {
		t.Errorf("oversized value accepted: %v", err)
	}

	s.SetCredential(ctx, "ANTHROPIC_API_KEY", "sk-test")
	s.SetSecret(ctx, "", "NORMAL", "v")
	list, _ := s.Secrets(ctx, nil)
	if len(list) != 1 || list[0].Name != "NORMAL" {
		t.Errorf("credentials leaked into the secrets list: %+v", list)
	}
	if env, _ := s.SecretEnv(ctx, SecretTarget{}); env["ANTHROPIC_API_KEY"] != "" {
		t.Error("credentials leaked into a secret layer")
	}
	if creds, _ := s.Credentials(ctx); creds["ANTHROPIC_API_KEY"] != "sk-test" {
		t.Errorf("credentials = %v", creds)
	}
	s.SetCredential(ctx, "ANTHROPIC_API_KEY", "")
	if creds, _ := s.Credentials(ctx); len(creds) != 0 {
		t.Errorf("credential not removed: %v", creds)
	}
}

func TestEnvironmentsAndMCPServers(t *testing.T) {
	s := open(t)
	e := Environment{ID: NewID(), Name: "Web stack", Repos: []RepoRef{{Repo: "acme/api"}, {Repo: "acme/web"}},
		InheritSecrets: true, Overrides: Overrides{Model: "opus"}}
	if err := s.CreateEnvironment(ctx, &e); err != nil {
		t.Fatal(err)
	}
	dup := Environment{ID: NewID(), Name: "web STACK"}
	if err := s.CreateEnvironment(ctx, &dup); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate name: err = %v", err)
	}
	got, _ := s.Environment(ctx, e.ID)
	if got.Overrides.Model != "opus" || len(got.Repos) != 2 || !got.InheritSecrets {
		t.Errorf("environment = %+v", got)
	}

	servers := []MCPServer{
		{ID: "m1", Name: "sentry", Type: "http", URL: "https://mcp.sentry.dev", Secret: map[string]string{"Authorization": "Bearer s"}, Enabled: true},
		{ID: "m2", Name: "local", Type: "stdio", Command: []string{"npx", "x"}, Scope: EnvScope(e.ID), Enabled: true},
		{ID: "m3", Name: "other", Type: "http", URL: "https://x", Scope: "acme/other", Enabled: true},
		{ID: "m4", Name: "off", Type: "http", URL: "https://x", Enabled: false},
	}
	for i := range servers {
		if err := s.CreateMCPServer(ctx, &servers[i]); err != nil {
			t.Fatal(err)
		}
	}
	var raw []byte
	s.r.QueryRow(`SELECT secret FROM mcp_servers WHERE id='m1'`).Scan(&raw)
	if strings.Contains(string(raw), "Bearer") {
		t.Error("MCP secret stored in plaintext")
	}
	m1, _ := s.MCPServer(ctx, "m1")
	if m1.Secret["Authorization"] != "Bearer s" || !slices.Equal(m1.SecretKeys, []string{"Authorization"}) {
		t.Errorf("m1 = %+v", m1)
	}
	applicable, _ := s.MCPServersFor(ctx, e.ID, []string{"acme/api"})
	var names []string
	for _, m := range applicable {
		names = append(names, m.Name)
	}
	if !slices.Equal(names, []string{"local", "sentry"}) {
		t.Errorf("servers for the environment = %v", names)
	}

	s.SetSecret(ctx, EnvScope(e.ID), "TOKEN", "x")
	if err := s.DeleteEnvironment(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.Secrets(ctx, ptr(EnvScope(e.ID))); len(left) != 0 {
		t.Errorf("environment secrets survived: %+v", left)
	}
	if _, err := s.MCPServer(ctx, "m2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("environment MCP server survived: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
