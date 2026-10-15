package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

var ctx = context.Background()

var jane = store.Author{Name: "Jane Dev", Email: "jane@example.com"}

func submit(t *testing.T, env *fake.Env, prompt string, mutate ...func(*session.Request)) (store.Session, store.Prompt) {
	t.Helper()
	r := session.Request{Prompt: prompt, Author: jane, Source: "test"}
	for _, f := range mutate {
		f(&r)
	}
	s, p, err := env.M.Submit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

func promptStatus(t *testing.T, env *fake.Env, id string) func() string {
	return func() string {
		p, err := env.DB.Prompt(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return p.Status
	}
}

func waitStatus(t *testing.T, env *fake.Env, promptID, want string) store.Prompt {
	t.Helper()
	get := promptStatus(t, env, promptID)
	fake.WaitFor(t, "prompt "+want, func() bool { return get() == want })
	p, _ := env.DB.Prompt(ctx, promptID)
	return p
}

func eventTypes(t *testing.T, env *fake.Env, sessionID string) []string {
	t.Helper()
	evs, err := env.DB.Events(ctx, sessionID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, e := range evs {
		types = append(types, e.Type)
	}
	return types
}

func TestPromptRunsToCompletion(t *testing.T) {
	env := fake.NewEnv(t)
	s, p := submit(t, env, "hello")
	p = waitStatus(t, env, p.ID, store.Done)

	if p.Result != "finished turn 1" || p.CostUSD != 0.25 {
		t.Errorf("prompt = %+v", p)
	}
	got, _ := env.M.Session(ctx, s.ID)
	fake.WaitFor(t, "session done", func() bool { got, _ = env.M.Session(ctx, s.ID); return got.Status == "done" })
	if got.Sandbox != "ready" || got.AgentSession != "convo-1" || got.CostUSD != 0.25 {
		t.Errorf("session = %+v", got)
	}
	want := []string{"prompt", "prompt.start", "sandbox", "sandbox", "sandbox", "text", "tool", "tool.result", "prompt.end"}
	if types := eventTypes(t, env, s.ID); !slices.Equal(types, want) {
		t.Errorf("events = %v\nwant     %v", types, want)
	}
	turn := env.Box.Turns[0]
	if turn.Env["GIT_AUTHOR_NAME"] != "Jane Dev" || turn.Env["GIT_COMMITTER_NAME"] != "Beethoven" {
		t.Errorf("commit identity env = %v", turn.Env)
	}
	if !slices.Contains(turn.Args, "--mcp-config") || turn.Dir != "/workspace" {
		t.Errorf("agent invocation = %v in %q", turn.Args, turn.Dir)
	}
}

func TestQueueIsFIFOAndResumesConversation(t *testing.T) {
	env := fake.NewEnv(t)
	s, p1 := submit(t, env, "first")
	p2, err := env.M.Enqueue(ctx, s.ID, session.PromptInput{Content: "second", Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	p3, _ := env.M.Enqueue(ctx, s.ID, session.PromptInput{Content: "third", Source: "test"})
	waitStatus(t, env, p3.ID, store.Done)

	for i, id := range []string{p1.ID, p2.ID, p3.ID} {
		p, _ := env.DB.Prompt(ctx, id)
		if want := []string{"finished turn 1", "finished turn 2", "finished turn 3"}[i]; p.Result != want {
			t.Errorf("prompt %d result = %q, want %q", i+1, p.Result, want)
		}
	}
	if got := env.Box.Turns[1].Args; !slices.Contains(got, "convo-1") {
		t.Errorf("second turn did not resume the conversation: %v", got)
	}
	if p2.Author != jane {
		t.Errorf("follow-up author = %+v, want the session author", p2.Author)
	}
}

func TestStopCancelsRunningAndQueued(t *testing.T) {
	env := fake.NewEnv(t)
	s, p1 := submit(t, env, "block forever")
	p2, _ := env.M.Enqueue(ctx, s.ID, session.PromptInput{Content: "never runs", Source: "test"})
	waitStatus(t, env, p1.ID, store.Running)

	if err := env.M.Stop(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, env, p1.ID, store.Cancelled)
	waitStatus(t, env, p2.ID, store.Cancelled)
	fake.WaitFor(t, "session stopped", func() bool { got, _ := env.M.Session(ctx, s.ID); return got.Status == "stopped" })

	// The session is still usable afterwards.
	p3, _ := env.M.Enqueue(ctx, s.ID, session.PromptInput{Content: "again", Source: "test"})
	waitStatus(t, env, p3.ID, store.Done)
}

func TestCancelOneQueuedPrompt(t *testing.T) {
	env := fake.NewEnv(t)
	s, p1 := submit(t, env, "block")
	p2, _ := env.M.Enqueue(ctx, s.ID, session.PromptInput{Content: "drop me", Source: "test"})
	p3, _ := env.M.Enqueue(ctx, s.ID, session.PromptInput{Content: "keep me", Source: "test"})
	waitStatus(t, env, p1.ID, store.Running)

	env.M.CancelPrompt(ctx, s.ID, p2.ID)
	waitStatus(t, env, p2.ID, store.Cancelled)
	if got := promptStatus(t, env, p1.ID)(); got != store.Running {
		t.Errorf("running prompt became %s", got)
	}
	env.M.CancelPrompt(ctx, s.ID, p1.ID) // stops the running one
	waitStatus(t, env, p1.ID, store.Cancelled)
	waitStatus(t, env, p3.ID, store.Done)
}

func TestFailedTurn(t *testing.T) {
	env := fake.NewEnv(t)
	s, p := submit(t, env, "please fail")
	p = waitStatus(t, env, p.ID, store.Failed)
	if !strings.Contains(p.Error, "simulated failure") {
		t.Errorf("error = %q", p.Error)
	}
	fake.WaitFor(t, "session failed", func() bool { got, _ := env.M.Session(ctx, s.ID); return got.Status == "failed" })
}

func TestLostConversationStartsFresh(t *testing.T) {
	env := fake.NewEnv(t)
	s, p1 := submit(t, env, "one")
	waitStatus(t, env, p1.ID, store.Done)

	// A sandbox rebuilt from scratch has no record of the conversation.
	env.Box.ForgetConversations()

	p2, _ := env.M.Enqueue(ctx, s.ID, session.PromptInput{Content: "two", Source: "test"})
	p2 = waitStatus(t, env, p2.ID, store.Done)
	if p2.Result != "finished turn 1" {
		t.Errorf("result = %q, want a fresh conversation", p2.Result)
	}
	if !slices.Contains(eventTypes(t, env, s.ID), "warning") {
		t.Error("expected a warning about the lost conversation")
	}
}

func TestIdleSandboxStopsAndResumes(t *testing.T) {
	env := fake.NewEnv(t, func(c *session.Config) { c.IdleTimeout = 30 * time.Millisecond })
	s, p1 := submit(t, env, "one")
	waitStatus(t, env, p1.ID, store.Done)
	fake.WaitFor(t, "idle stop", func() bool { got, _ := env.M.Session(ctx, s.ID); return got.Sandbox == "stopped" })
	if env.Box.Running("beethoven-" + s.ID) {
		t.Fatal("sandbox still running after idle stop")
	}

	p2, _ := env.M.Enqueue(ctx, s.ID, session.PromptInput{Content: "two", Source: "test"})
	p2 = waitStatus(t, env, p2.ID, store.Done)
	if p2.Result != "finished turn 2" {
		t.Errorf("result = %q; conversation not resumed after restart", p2.Result)
	}
	boots := 0
	for _, c := range env.Box.Calls() {
		if strings.HasPrefix(c, "boot ") {
			boots++
		}
	}
	if boots != 2 {
		t.Errorf("boots = %d, want 2 (one per start)", boots)
	}
}

func TestSandboxStoppedBehindOurBack(t *testing.T) {
	env := fake.NewEnv(t)
	s, p1 := submit(t, env, "one")
	waitStatus(t, env, p1.ID, store.Done)
	env.Box.Stop(ctx, "beethoven-"+s.ID) // e.g. Docker restarted

	p2, _ := env.M.Enqueue(ctx, s.ID, session.PromptInput{Content: "two", Source: "test"})
	if got := waitStatus(t, env, p2.ID, store.Done); got.Result != "finished turn 2" {
		t.Errorf("result = %q", got.Result)
	}
	if boots := strings.Count(strings.Join(env.Box.Calls(), "\n"), "boot "); boots != 2 {
		t.Errorf("boots = %d, want the workspace booted again", boots)
	}
}

func TestBootFailureFailsPromptAndRetries(t *testing.T) {
	env := fake.NewEnv(t)
	env.Box.BootErr = true
	s, p1 := submit(t, env, "one", func(r *session.Request) { r.Repos = []store.RepoRef{{Repo: "acme/missing"}} })
	p1 = waitStatus(t, env, p1.ID, store.Failed)
	if !strings.Contains(p1.Error, "boot failed during clone") || !strings.Contains(p1.Error, "repository not found") {
		t.Errorf("error = %q", p1.Error)
	}
	env.Box.BootErr = false
	p2, _ := env.M.Enqueue(ctx, s.ID, session.PromptInput{Content: "two", Source: "test"})
	waitStatus(t, env, p2.ID, store.Done)
}

func TestContinuationAndDedupKeys(t *testing.T) {
	env := fake.NewEnv(t)
	s1, p1 := submit(t, env, "one", func(r *session.Request) { r.Key = "slack:thread-1"; r.DedupKey = "evt-1" })
	s2, p2 := submit(t, env, "two", func(r *session.Request) { r.Key = "slack:thread-1"; r.DedupKey = "evt-2" })
	_, p3 := submit(t, env, "two again", func(r *session.Request) { r.Key = "slack:thread-1"; r.DedupKey = "evt-2" })
	if s1.ID != s2.ID {
		t.Errorf("continuation key started a new session")
	}
	if p3.ID != p2.ID {
		t.Errorf("duplicate delivery queued a second prompt")
	}
	waitStatus(t, env, p2.ID, store.Done)
	if ps, _ := env.DB.Prompts(ctx, s1.ID); len(ps) != 2 || ps[0].ID != p1.ID {
		t.Errorf("prompts = %+v", ps)
	}
}

func TestChildSessions(t *testing.T) {
	env := fake.NewEnv(t)
	parent, _ := submit(t, env, "parent", func(r *session.Request) { r.Repos = []store.RepoRef{{Repo: "acme/app", BaseBranch: "dev"}} })
	child, cp := submit(t, env, "child", func(r *session.Request) { r.ParentID = parent.ID })
	if len(child.Repos) != 1 || child.Repos[0] != (store.RepoRef{Repo: "acme/app", BaseBranch: "dev"}) || child.Depth != 1 || child.Branch == parent.Branch {
		t.Errorf("child = %+v", child)
	}
	waitStatus(t, env, cp.ID, store.Done)
	fake.WaitFor(t, "child event on parent", func() bool { return slices.Contains(eventTypes(t, env, parent.ID), "child") })

	grandchild, _ := submit(t, env, "grandchild", func(r *session.Request) { r.ParentID = child.ID })
	_, _, err := env.M.Submit(ctx, session.Request{Prompt: "too deep", ParentID: grandchild.ID, Source: "test"})
	if !errors.Is(err, session.ErrDepth) {
		t.Errorf("err = %v, want ErrDepth", err)
	}
}

func TestOpenPR(t *testing.T) {
	env := fake.NewEnv(t)
	s, p := submit(t, env, "work", func(r *session.Request) { r.Repos = []store.RepoRef{{Repo: "acme/app"}} })
	waitStatus(t, env, p.ID, store.Done)

	url, err := env.M.OpenPR(ctx, s.ID, "", "", "body", nil)
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://github.com/acme/app/pull/1" {
		t.Errorf("url = %q", url)
	}
	pr := env.SCM.PRs[0]
	if pr.Head != s.Branch || pr.Base != "main" || pr.Title != s.Title {
		t.Errorf("pr = %+v", pr)
	}
	push := env.Box.Pushes[0]
	if push.Env["GH_TOKEN"] != "test-token" || push.Env["BRANCH"] != s.Branch {
		t.Errorf("push env = %v", push.Env)
	}
	if got, _ := env.M.Session(ctx, s.ID); got.Repos[0].PRURL != url {
		t.Errorf("pr_url = %q", got.Repos[0].PRURL)
	}
	if !slices.Contains(eventTypes(t, env, s.ID), "artifact") {
		t.Error("no artifact event")
	}

	scratch, sp := submit(t, env, "no repo")
	waitStatus(t, env, sp.ID, store.Done)
	if _, err := env.M.OpenPR(ctx, scratch.ID, "", "", "", nil); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}

// restart simulates a crash: a new manager over the same database, whose
// sandboxes are still up.
func restart(t *testing.T, env *fake.Env, sessionIDs ...string) *fake.Env {
	t.Helper()
	env.M.Shutdown(5 * time.Second)
	env2 := &fake.Env{DB: env.DB, Box: fake.NewProvider(), SCM: env.SCM}
	for _, id := range sessionIDs {
		env2.Box.Start(ctx, sandboxSpec(id))
	}
	env2.M = newManager(t, env2)
	if err := env2.M.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return env2
}

func TestRecoveryAfterRestart(t *testing.T) {
	env := fake.NewEnv(t)
	busy, p := submit(t, env, "block")
	idle, ip := submit(t, env, "quick")
	waitStatus(t, env, p.ID, store.Running)
	waitStatus(t, env, ip.ID, store.Done)
	queued, _ := env.M.Enqueue(ctx, busy.ID, session.PromptInput{Content: "after restart", Source: "test"})

	env2 := restart(t, env, busy.ID, idle.ID)
	if got := waitStatus(t, env2, p.ID, store.Failed); !strings.Contains(got.Error, "interrupted") {
		t.Errorf("error = %q", got.Error)
	}
	if got, _ := env2.M.Session(ctx, idle.ID); got.Status != "done" {
		t.Errorf("untouched session status = %q", got.Status)
	}
	waitStatus(t, env2, queued.ID, store.Done)
	calls := env2.Box.Calls()
	if !slices.Contains(calls, "boot beethoven-"+busy.ID) {
		t.Errorf("queued work ran without re-booting the workspace: %v", calls)
	}
	fake.WaitFor(t, "idle leftover stopped", func() bool { return !env2.Box.Running("beethoven-" + idle.ID) })
	if got, _ := env2.M.Session(ctx, idle.ID); got.Sandbox != "stopped" {
		t.Errorf("leftover sandbox state = %q", got.Sandbox)
	}
}

func TestTokens(t *testing.T) {
	env := fake.NewEnv(t)
	s, _ := submit(t, env, "x")
	tok := env.M.Token(s.ID)
	if got, err := env.M.Authenticate(ctx, tok); err != nil || got.ID != s.ID {
		t.Fatalf("Authenticate(valid) = %v, %v", got.ID, err)
	}
	for _, bad := range []string{"", s.ID, s.ID + ".00", tok + "0", "other." + strings.Split(tok, ".")[1]} {
		if _, err := env.M.Authenticate(ctx, bad); !errors.Is(err, session.ErrUnauthorized) {
			t.Errorf("Authenticate(%q) err = %v", bad, err)
		}
	}
}

func TestArchiveKeepsTheSession(t *testing.T) {
	env := fake.NewEnv(t)
	s, p := submit(t, env, "x")
	waitStatus(t, env, p.ID, store.Done)

	if err := env.M.Archive(ctx, s.ID, true); err != nil {
		t.Fatal(err)
	}
	fake.WaitFor(t, "archive stops sandbox", func() bool { got, _ := env.M.Session(ctx, s.ID); return got.Archived && got.Sandbox == "stopped" })
	p2, _ := env.M.Enqueue(ctx, s.ID, session.PromptInput{Content: "wake up", Source: "test"})
	waitStatus(t, env, p2.ID, store.Done)
	if got, _ := env.M.Session(ctx, s.ID); got.Archived {
		t.Error("prompting did not unarchive")
	}

	if err := env.M.Archive(ctx, s.ID, true); err != nil {
		t.Fatal(err)
	}
	if ps, _ := env.DB.Prompts(ctx, s.ID); len(ps) != 2 {
		t.Errorf("an archived session kept %d of its 2 prompts", len(ps))
	}
	if slices.Contains(env.Box.Calls(), "destroy beethoven-"+s.ID) {
		t.Error("archiving destroyed the sandbox; it should only stop it")
	}
}

func TestLiveEventsReachSubscribers(t *testing.T) {
	env := fake.NewEnv(t)
	all := env.M.Hub().Subscribe(session.AllSessions)
	defer env.M.Hub().Unsubscribe(all)
	s, p := submit(t, env, "hello")
	sub := env.M.Hub().Subscribe(s.ID)
	defer env.M.Hub().Unsubscribe(sub)
	waitStatus(t, env, p.ID, store.Done)

	var sawDelta bool
	deadline := time.After(2 * time.Second)
	for !sawDelta {
		select {
		case e := <-sub.C:
			sawDelta = e.Type == "delta" && e.Seq == 0
		case <-deadline:
			t.Fatal("no ephemeral delta delivered")
		}
	}
	select {
	case e := <-all.C:
		var got store.Session
		if e.Type != session.EvSession || json.Unmarshal(e.Data, &got) != nil || got.ID != s.ID {
			t.Errorf("global event = %+v", e)
		}
	case <-time.After(time.Second):
		t.Error("no global session event")
	}
}
