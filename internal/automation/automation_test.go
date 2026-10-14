package automation

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

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
	if _, _, err := svc.Create(ctx, Spec{Name: "n", Prompt: "p", Schedule: "not cron"}); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("bad cron: err = %v", err)
	}
	if _, _, err := svc.Create(ctx, Spec{Name: "", Prompt: "p"}); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("no name: err = %v", err)
	}
	a, token, err := svc.Create(ctx, Spec{Name: "nightly", Prompt: "p", Schedule: "CRON_TZ=America/New_York 0 9 * * *", Enabled: true})
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
	a, _, _ := svc.Create(ctx, Spec{Name: "n", Prompt: "block", Reuse: true, Enabled: true})
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
	a, _, _ := svc.Create(ctx, Spec{Name: "n", Prompt: "fail", Enabled: true})
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
	a, _, _ := svc.Create(ctx, Spec{Name: "n", Prompt: "scheduled work", Schedule: "*/5 * * * *", Enabled: true})
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
