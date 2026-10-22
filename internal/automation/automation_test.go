package automation

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/brdsmth/beethoven/internal/event"
	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

var ctx = context.Background()

func setup(t *testing.T) (*Service, *fake.Env) {
	env := fake.NewEnv(t)
	return New(env.M, slog.New(slog.NewTextHandler(io.Discard, nil))), env
}

func waitIdle(t *testing.T, env *fake.Env, a store.Automation) {
	t.Helper()
	fake.WaitFor(t, "automation idle", func() bool { busy, _ := env.DB.AutomationBusy(ctx, a.ID); return !busy })
}

func TestCreateValidates(t *testing.T) {
	svc, _ := setup(t)
	if _, _, err := svc.Create(ctx, Spec{Name: "n", Prompt: "p", Schedule: "not cron"}, ""); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("bad cron: err = %v", err)
	}
	if _, _, err := svc.Create(ctx, Spec{Name: "", Prompt: "p"}, ""); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("no name: err = %v", err)
	}
	a, token, err := svc.Create(ctx, Spec{Name: "nightly", Prompt: "p", Schedule: "CRON_TZ=America/New_York 0 9 * * *", Enabled: true}, "")
	if err != nil || a.NextRunAt == nil || a.NextRunAt.Before(time.Now()) || len(token) < 32 {
		t.Fatalf("create = %+v, %q, %v", a, token, err)
	}
	if _, err := svc.CheckHook(ctx, a.ID, token); err != nil {
		t.Errorf("valid hook token rejected: %v", err)
	}
	if _, err := svc.CheckHook(ctx, a.ID, token+"x"); !errors.Is(err, session.ErrUnauthorized) {
		t.Errorf("bad hook token: err = %v", err)
	}
}

func TestFireBusyAndReuse(t *testing.T) {
	svc, env := setup(t)
	a, _, _ := svc.Create(ctx, Spec{Name: "n", Prompt: "block", Reuse: true, Enabled: true}, "")
	s1, p1, err := svc.Fire(ctx, a, "manual", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Fire(ctx, a, "manual", "", ""); !errors.Is(err, ErrBusy) {
		t.Errorf("overlapping run: err = %v", err)
	}
	env.M.Stop(ctx, s1.ID)
	waitIdle(t, env, a)

	a.Prompt = "quick"
	svc.db.SaveAutomation(ctx, &a)
	s2, p2, err := svc.Fire(ctx, a, "webhook", `{"event":"push"}`, "delivery-1")
	if err != nil {
		t.Fatal(err)
	}
	if s2.ID != s1.ID {
		t.Error("reuse=true started a new session")
	}
	if p2.ID == p1.ID || p2.AutomationID != a.ID || p2.Author.Name != "Test Owner" {
		t.Errorf("run prompt = %+v", p2)
	}
	if want := "quick\n\nThe webhook that triggered this run delivered:\n<payload>\n{\"event\":\"push\"}\n</payload>"; p2.Content != want {
		t.Errorf("content = %q", p2.Content)
	}
	waitIdle(t, env, a)
	if _, p3, _ := svc.Fire(ctx, a, "webhook", `{}`, "delivery-1"); p3.ID != p2.ID {
		t.Error("redelivered webhook fired twice")
	}
}

func TestPausesAfterRepeatedFailures(t *testing.T) {
	svc, env := setup(t)
	a, _, _ := svc.Create(ctx, Spec{Name: "n", Prompt: "fail", Enabled: true}, "")
	for i := 1; i <= MaxFailures; i++ {
		if _, _, err := svc.Fire(ctx, a, "manual", "", ""); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		fake.WaitFor(t, "failure counted", func() bool { got, _ := env.DB.Automation(ctx, a.ID); return got.Failures == i })
	}
	got, _ := env.DB.Automation(ctx, a.ID)
	if got.Enabled {
		t.Fatal("automation still enabled after repeated failures")
	}
	if _, _, err := svc.Fire(ctx, got, "schedule", "", ""); !errors.Is(err, ErrDisabled) {
		t.Errorf("paused automation fired: %v", err)
	}

	spec := Spec{Name: "n", Prompt: "fine now", Enabled: true}
	if got, _ = svc.Update(ctx, a.ID, spec); got.Failures != 0 || !got.Enabled {
		t.Errorf("re-enabled = %+v", got)
	}
}

func TestTickFiresDueSchedulesOnce(t *testing.T) {
	svc, env := setup(t)
	a, _, _ := svc.Create(ctx, Spec{Name: "n", Prompt: "scheduled work", Schedule: "*/5 * * * *", Enabled: true}, "")
	past := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	a.NextRunAt = &past
	svc.db.SaveAutomation(ctx, &a)

	svc.tick(ctx, time.Now())
	svc.tick(ctx, time.Now())
	runs, _ := env.DB.AutomationRuns(ctx, a.ID, 10)
	if len(runs) != 1 || runs[0].Source != "schedule" {
		t.Fatalf("runs = %+v", runs)
	}
	got, _ := env.DB.Automation(ctx, a.ID)
	if !got.NextRunAt.After(time.Now()) {
		t.Errorf("next run not advanced: %v", got.NextRunAt)
	}
}

func TestDispatchEvents(t *testing.T) {
	svc, env := setup(t)
	labeled := &event.Trigger{Event: event.IssueLabeled, Conditions: []event.Condition{{Type: event.CondAddedLabel, Values: []string{"agent"}}}}
	if _, _, err := svc.Create(ctx, Spec{Name: "n", Prompt: "p", Trigger: labeled, Enabled: true}, ""); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("trigger without a target: err = %v", err)
	}
	fix, _, err := svc.Create(ctx, Spec{Name: "Fix labeled issues", Prompt: "Fix the issue.", Repos: []store.RepoRef{{Repo: "acme/app"}},
		Trigger: labeled, Reuse: true, Enabled: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	review, _, _ := svc.Create(ctx, Spec{Name: "Review", Prompt: "Review it.", Repos: []store.RepoRef{{Repo: "acme/app"}},
		Trigger: &event.Trigger{Event: event.PROpened}, Enabled: true}, "")
	if got, _ := env.DB.Automation(ctx, fix.ID); Trigger(got).Event != event.IssueLabeled || len(Trigger(got).Conditions) != 1 {
		t.Fatalf("stored trigger = %+v", Trigger(got))
	}

	issue := event.Event{Host: "github", Kind: event.IssueLabeled, Repo: "acme/app", Number: 7, Title: "Crash on save",
		Label: "agent", Labels: []string{"agent"}, Subject: "issue:7", Occurrence: "issue:7:labeled:agent"}
	started, err := svc.Dispatch(ctx, issue)
	if err != nil || len(started) != 1 || started[0].AutomationID != fix.ID {
		t.Fatalf("dispatch = %+v, %v", started, err)
	}
	if !strings.Contains(started[0].Content, "Fix the issue.") || !strings.Contains(started[0].Content, "#7 Crash on save") {
		t.Errorf("prompt = %q", started[0].Content)
	}
	sess, _ := env.DB.Session(ctx, started[0].SessionID)
	if !strings.Contains(sess.Title, "#7") || len(sess.Repos) != 1 || sess.Repos[0].Repo != "acme/app" {
		t.Errorf("session = %+v", sess)
	}

	// A redelivery fires nothing new; a later event on the same issue joins its session.
	if again, _ := svc.Dispatch(ctx, issue); len(again) != 1 || again[0].ID != started[0].ID {
		t.Errorf("redelivery = %+v", again)
	}
	issue.Occurrence = "issue:7:labeled:agent:2"
	if later, _ := svc.Dispatch(ctx, issue); len(later) != 1 || later[0].SessionID != sess.ID || later[0].ID == started[0].ID {
		t.Errorf("second event on the issue = %+v", later)
	}

	// Other labels, other repositories and other kinds don't match.
	for _, e := range []event.Event{
		{Host: "github", Kind: event.IssueLabeled, Repo: "acme/app", Number: 8, Label: "bug", Subject: "issue:8", Occurrence: "a"},
		{Host: "github", Kind: event.IssueLabeled, Repo: "acme/other", Number: 8, Label: "agent", Subject: "issue:8", Occurrence: "b"},
		{Host: "github", Kind: event.IssueOpened, Repo: "acme/app", Number: 8, Subject: "issue:8", Occurrence: "c"},
	} {
		if got, _ := svc.Dispatch(ctx, e); len(got) != 0 {
			t.Errorf("%+v fired %d runs", e, len(got))
		}
	}

	// A pull request's run starts from its branch.
	pr := event.Event{Host: "github", Kind: event.PROpened, Repo: "acme/app", Number: 3, Branch: "feature", Base: "main",
		Subject: "pr:3", Occurrence: "pr:3:opened"}
	got, err := svc.Dispatch(ctx, pr)
	if err != nil || len(got) != 1 || got[0].AutomationID != review.ID {
		t.Fatalf("pull request dispatch = %+v, %v", got, err)
	}
	if s, _ := env.DB.Session(ctx, got[0].SessionID); s.Repos[0].BaseBranch != "feature" {
		t.Errorf("review session repos = %+v", s.Repos)
	}
}

// A run finishing doesn't undo an edit made meanwhile: only the count of
// failures and the pause change.
func TestRunFinishingKeepsEdits(t *testing.T) {
	svc, env := setup(t)
	a, _, _ := svc.Create(ctx, Spec{Name: "n", Prompt: "p", Enabled: true}, "")
	_, p, err := svc.Fire(ctx, a, "manual", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(ctx, a.ID, Spec{Name: "renamed", Prompt: "p", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	finished, _ := env.DB.Prompt(ctx, p.ID)
	finished.Status = store.Done
	svc.PromptFinished(ctx, store.Session{}, finished)
	if got, _ := env.DB.Automation(ctx, a.ID); got.Name != "renamed" || got.Enabled {
		t.Errorf("the edit was undone: %+v", got)
	}
	waitIdle(t, env, a)
}
