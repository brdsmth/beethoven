package session_test

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brdsmth/beethoven/internal/agent"
	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/sandbox"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

// TestDocker drives real containers: a fake `claude` (testdata/fakeagent)
// works on a clone of a local bare repository standing in for GitHub.
// It touches only the containers it creates.
func TestDocker(t *testing.T) {
	env, remote := dockerEnv(t)
	bare := bareRepo(t, remote, "acme/demo")
	m, db, scm := env.M, env.DB, env.SCM

	s, p := submit(t, env, "COMMIT add a feature", func(r *session.Request) { r.Repos = []store.RepoRef{{Repo: "acme/demo"}} })
	cleanupContainer(t, s.ID)
	slowWait(t, env, p.ID, store.Done)
	evs, _ := db.Events(ctx, s.ID, 0, 1000)
	if !slices.ContainsFunc(evs, func(e store.Event) bool {
		return e.Type == "sandbox" && strings.Contains(string(e.Data), `"clone demo"`)
	}) {
		t.Error("no clone phase reported")
	}

	// Pull request: the server pushes from inside the sandbox.
	if _, err := m.OpenPR(ctx, s.ID, "", "Add a feature", "", nil); err != nil {
		t.Fatal(err)
	}
	log := run(t, "", "git", "--git-dir", bare, "log", "-1", "--format=%an <%ae> | %cn | %s", s.Branch)
	if got := strings.TrimSpace(log); got != "Jane Dev <jane@example.com> | Beethoven | fake turn 1" {
		t.Errorf("pushed commit = %q", got)
	}
	if pr := scm.PRs[0]; pr.Head != s.Branch || pr.Base != "main" {
		t.Errorf("pr = %+v", pr)
	}

	// Stop kills the agent's whole process tree inside the container.
	p2, _ := m.Enqueue(ctx, s.ID, session.PromptInput{Content: "SLEEP 120", Source: "test"})
	fake.WaitFor(t, "sleep running", func() bool {
		out, _ := exec.Command("docker", "exec", "beethoven-"+s.ID, "ps", "-o", "args").Output()
		return strings.Contains(string(out), "sleep 120")
	})
	start := time.Now()
	m.Stop(ctx, s.ID)
	slowWait(t, env, p2.ID, store.Cancelled)
	if d := time.Since(start); d > 12*time.Second {
		t.Errorf("stop took %s", d)
	}
	if out, _ := exec.Command("docker", "exec", "beethoven-"+s.ID, "ps", "-o", "args").Output(); strings.Contains(string(out), "sleep 120") {
		t.Errorf("agent processes survived stop:\n%s", out)
	}

	// Idle: the container stops, and the next prompt restarts it and
	// resumes the same conversation from the preserved filesystem.
	fake.WaitFor(t, "idle stop", func() bool { got, _ := m.Session(ctx, s.ID); return got.Sandbox == "stopped" })
	if out := run(t, "", "docker", "inspect", "-f", "{{.State.Running}}", "beethoven-"+s.ID); strings.TrimSpace(out) != "false" {
		t.Errorf("container running after idle stop: %s", out)
	}
	p3, _ := m.Enqueue(ctx, s.ID, session.PromptInput{Content: "and again", Source: "test"})
	if got := slowWait(t, env, p3.ID, store.Done); got.Result != "Done with turn 3." {
		t.Errorf("result after restart = %q, want the conversation resumed at turn 3", got.Result)
	}

	// Attachments: saved in the agent's home, byte for byte.
	p4, _ := m.Enqueue(ctx, s.ID, session.PromptInput{Content: "look", Source: "test",
		Attachments: []store.Attachment{{Name: "notes.txt", Data: []byte("line one\nline two\n")}}})
	slowWait(t, env, p4.ID, store.Done)
	if got := run(t, "", "docker", "exec", "beethoven-"+s.ID, "sh", "-c", `cat "$HOME/attachments/$0/notes.txt"`, p4.ID); got != "line one\nline two\n" {
		t.Errorf("attachment in the sandbox = %q", got)
	}

	run(t, "", "docker", "rm", "-f", "-v", "beethoven-"+s.ID)
}

// TestDockerEnvironment boots a two-repository environment with scripts,
// a scoped secret and resource limits, then opens a pull request in the
// second repository.
func TestDockerEnvironment(t *testing.T) {
	env, remote := dockerEnv(t)
	api := bareRepo(t, remote, "acme/api")
	web := bareRepo(t, remote, "acme/web")
	// A repository that names a capability the catalog lacks, and looks
	// like it needs Go.
	seed := t.TempDir()
	run(t, "", "git", "clone", "-q", api, seed)
	os.MkdirAll(filepath.Join(seed, ".beethoven"), 0o755)
	os.WriteFile(filepath.Join(seed, ".beethoven/capabilities"), []byte("# toolchains\nnope\n"), 0o644)
	os.WriteFile(filepath.Join(seed, "go.mod"), []byte("module example.com/api\n"), 0o644)
	run(t, seed, "git", "add", ".")
	run(t, seed, "git", "-c", "user.name=seed", "-c", "user.email=seed@example.com", "commit", "-q", "-m", "capabilities")
	run(t, seed, "git", "push", "-q", "origin", "main")
	e := environment(t, env, store.Environment{
		Name:        "Stack",
		Repos:       []store.RepoRef{{Repo: "acme/api"}, {Repo: "acme/web", BaseBranch: "main"}},
		SetupScript: `printf '%s %s\n' "$DEPLOY_TOKEN" "$(pwd)" > "$HOME/setup-ran"`,
		StartScript: `touch /tmp/started`,
		Overrides:   store.Overrides{CPUs: 1.5, MemoryMB: 512},
	})
	env.DB.SetSecret(ctx, store.EnvScope(e.ID), "DEPLOY_TOKEN", "tok-123")

	s, p := submit(t, env, "look around", func(r *session.Request) { r.EnvironmentID = e.ID })
	cleanupContainer(t, s.ID)
	slowWait(t, env, p.ID, store.Done)

	name := "beethoven-" + s.ID
	sh := func(script string) string {
		return strings.TrimSpace(run(t, "", "docker", "exec", name, "sh", "-c", script))
	}
	for _, dir := range []string{"api", "web"} {
		if got := sh("git -C /workspace/" + dir + " branch --show-current"); got != s.Branch {
			t.Errorf("%s is on %q, want %q", dir, got, s.Branch)
		}
	}
	if got := sh(`cat "$HOME/setup-ran"`); got != "tok-123 /workspace" {
		t.Errorf("setup script saw %q", got)
	}
	sh("test -f /tmp/started")
	// Capabilities: the catalog arrived, the unknown one was reported and Go was suggested.
	sh(`test -f "$HOME/.beethoven/catalog/expo/SKILL.md"`)
	evs, _ := env.DB.Events(ctx, s.ID, 0, 1000)
	if !slices.ContainsFunc(evs, func(e store.Event) bool {
		return e.Type == "warning" && strings.Contains(string(e.Data), `unknown capability \"nope\"`)
	}) {
		t.Error("the unknown capability wasn't reported")
	}
	if !slices.ContainsFunc(evs, func(e store.Event) bool {
		return e.Type == session.EvCapabilities && strings.Contains(string(e.Data), `"suggested":["go"]`)
	}) {
		t.Errorf("go wasn't suggested: %v", evs)
	}
	if got := strings.TrimSpace(run(t, "", "docker", "inspect", "-f", "{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}}", name)); got != "1500000000 536870912" {
		t.Errorf("resources = %s", got)
	}

	sh(`cd /workspace/web && echo change > change.txt && git add change.txt && git -c user.name=Agent -c user.email=agent@example.com commit -qm "web change"`)
	if _, err := env.M.OpenPR(ctx, s.ID, "acme/web", "Web change", "", nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(run(t, "", "git", "--git-dir", web, "log", "-1", "--format=%s", s.Branch)); got != "web change" {
		t.Errorf("pushed %q", got)
	}
	if pr := env.SCM.PRs[0]; pr.Repo != "acme/web" || pr.Base != "main" {
		t.Errorf("pr = %+v", pr)
	}
}

// dockerEnv is a manager over real containers using the fake agent image,
// with remote repositories served from a local directory mounted at /remote.
func dockerEnv(t *testing.T) (*fake.Env, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("uses Docker")
	}
	if exec.Command("docker", "info").Run() != nil {
		t.Skip("Docker is not available")
	}
	run(t, "", "docker", "build", "-q", "-t", "beethoven-fake:test", "testdata/fakeagent")
	remote := t.TempDir()
	db, err := store.Open(filepath.Join(t.TempDir(), "e2e.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	scm := &fake.SCM{}
	m := session.New(session.Config{Image: "beethoven-fake:test", IdleTimeout: 3 * time.Second, TurnTimeout: time.Minute,
		ToolsURL: "http://host.docker.internal:1/mcp", TokenKey: []byte("k")},
		db, &sandbox.Docker{Args: []string{"-v", remote + ":/remote"}}, agent.Claude{}, scm,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		m.Shutdown(10 * time.Second)
		db.Close()
	})
	return &fake.Env{M: m, DB: db, SCM: scm}, remote
}

// bareRepo creates remote/<repo>.git with one commit on main.
func bareRepo(t *testing.T, remote, repo string) string {
	t.Helper()
	bare := filepath.Join(remote, repo+".git")
	seed := t.TempDir()
	run(t, "", "git", "init", "-q", "--bare", "-b", "main", bare)
	run(t, seed, "git", "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(seed, "README.md"), []byte(repo+"\n"), 0o644)
	run(t, seed, "git", "add", ".")
	run(t, seed, "git", "-c", "user.name=seed", "-c", "user.email=seed@example.com", "commit", "-q", "-m", "init")
	run(t, seed, "git", "push", "-q", bare, "main")
	return bare
}

// cleanupContainer removes a test session's container, whatever happens.
func cleanupContainer(t *testing.T, sessionID string) {
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", "beethoven-"+sessionID).Run() })
}

func slowWait(t *testing.T, env *fake.Env, promptID, want string) store.Prompt {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		p, err := env.DB.Prompt(ctx, promptID)
		if err == nil && p.Status == want {
			return p
		}
		if err == nil && p.Status != store.Queued && p.Status != store.Running {
			t.Fatalf("prompt %s ended %s (%s), want %s", promptID, p.Status, p.Error, want)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for prompt %s to be %s", promptID, want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}
