package insights_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/insights"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

var (
	ctx  = context.Background()
	jane = store.Author{Name: "Jane Dev", Email: "jane@example.com"}
	bob  = store.Author{Name: "Bob Ops", Email: "bob@example.com"}
)

// run submits a request and waits for its prompt to finish.
func run(t *testing.T, env *fake.Env, r session.Request) store.Session {
	t.Helper()
	s, p, err := env.M.Submit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, env, p.ID)
	return s
}

func wait(t *testing.T, env *fake.Env, promptID string) {
	t.Helper()
	fake.WaitFor(t, "prompt to finish", func() bool {
		p, _ := env.DB.Prompt(ctx, promptID)
		return p.Status != store.Queued && p.Status != store.Running
	})
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestQualityMeasures(t *testing.T) {
	env := fake.NewEnv(t)
	app := []store.RepoRef{{Repo: "acme/app"}}

	// First try: one cycle, and its pull request merged. Its agent spawned a helper.
	first := run(t, env, session.Request{Prompt: "fix the login bug", Repos: app, Author: jane, Source: "web"})
	run(t, env, session.Request{Prompt: "help with it", ParentID: first.ID, Author: jane, Source: "agent"})
	if _, err := env.M.OpenPR(ctx, first.ID, "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	// Two cycles.
	twice := run(t, env, session.Request{Prompt: "tidy the README", Repos: app, Author: bob, Source: "slack"})
	p, err := env.M.Enqueue(ctx, twice.ID, session.PromptInput{Content: "also the CHANGELOG", Author: bob, Source: "slack"})
	if err != nil {
		t.Fatal(err)
	}
	wait(t, env, p.ID)
	// Failed.
	run(t, env, session.Request{Prompt: "fail at this", Author: jane, Source: "web"})
	// Done, but its pull request was closed without merging.
	rejected := run(t, env, session.Request{Prompt: "rename things", Repos: []store.RepoRef{{Repo: "acme/web"}}, Author: jane, Source: "web"})
	if _, err := env.M.OpenPR(ctx, rejected.ID, "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	// An automation run: not one of the sessions people started.
	run(t, env, session.Request{Prompt: "nightly check", Repos: app, AutomationID: "a1", Source: "schedule", Author: jane})

	if _, err := env.DB.TrackNewPullRequests(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, sid := range []string{first.ID, rejected.ID} {
		prs, _ := env.DB.SessionPullRequests(ctx, sid)
		pr := prs[0]
		pr.OpenedAt, pr.SyncedAt = now.Add(-2*time.Hour), now
		if sid == first.ID {
			pr.State, pr.MergedAt = "merged", &now
		} else {
			pr.State, pr.ClosedAt = "closed", &now
		}
		if err := env.DB.SavePullRequest(ctx, pr); err != nil {
			t.Fatal(err)
		}
	}

	r, err := insights.Compute(ctx, env.DB, 7, now, time.UTC, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Sessions != 4 || r.Settled != 4 {
		t.Fatalf("sessions %d, settled %d; want the 4 people started", r.Sessions, r.Settled)
	}
	if !near(r.FirstTryRate, 0.25) || !near(r.SuccessRate, 0.5) || !near(r.Cycles, 1.25) {
		t.Errorf("first try %v, success %v, cycles %v; want 0.25, 0.5, 1.25", r.FirstTryRate, r.SuccessRate, r.Cycles)
	}
	if c := r.CycleCounts; c[0].N != 3 || c[1].N != 1 || c[2].N != 0 || c[3].N != 0 {
		t.Errorf("cycle counts %+v", c)
	}
	if r.Failed != 1 || len(r.Failures) != 1 || r.Failures[0].Label != "simulated failure" {
		t.Errorf("failed %d: %+v", r.Failed, r.Failures)
	}
	if a := r.Automations; a.Runs != 1 || a.Succeeded != 1 {
		t.Errorf("automations %+v", a)
	}

	pr := r.PullRequests
	if pr.Opened != 2 || pr.Merged != 1 || pr.Closed != 1 || pr.AcceptanceRate == nil || !near(*pr.AcceptanceRate, 0.5) {
		t.Errorf("pull requests %+v", pr)
	}
	if !near(pr.MedianToMergeSecs, 7200) {
		t.Errorf("median to merge %v", pr.MedianToMergeSecs)
	}
	spent := func(id string) float64 { s, _ := env.DB.Session(ctx, id); return s.CostUSD }
	children, _ := env.DB.Sessions(ctx, store.SessionFilter{ParentID: first.ID})
	if want := spent(first.ID) + spent(children[0].ID); want == 0 || !near(pr.CostPerMerged, want) {
		t.Errorf("cost per merged %v, want the run's %v, its spawned session included", pr.CostPerMerged, want)
	}
	all, _ := env.DB.Sessions(ctx, store.SessionFilter{Limit: 100})
	total := 0.0
	for _, s := range all {
		total += s.CostUSD
	}
	if !near(r.Spend, total) {
		t.Errorf("spend %v, want %v", r.Spend, total)
	}

	if len(r.Days) != 7 {
		t.Fatalf("%d days", len(r.Days))
	}
	if d := r.Days[6]; d.Sessions != 4 || d.FirstTry != 1 || d.MoreCycles != 1 || d.Unsuccessful != 2 || d.Merged != 1 {
		t.Errorf("today = %+v", d)
	}
	if len(r.BySource) != 2 || r.BySource[0].Name != "web" || r.BySource[0].Sessions != 3 || r.BySource[1].Name != "slack" {
		t.Errorf("by source %+v", r.BySource)
	}
	if g := r.ByRepo; len(g) != 3 || g[0].Name != "acme/app" || g[0].Merged != 1 || !near(g[0].FirstTryRate, 0.5) {
		t.Errorf("by repository %+v", g)
	}
	if g := r.ByPerson; len(g) != 2 || g[0].Name != "Jane Dev" || g[0].Sessions != 3 {
		t.Errorf("by person %+v", g)
	}
}

func TestNothingYet(t *testing.T) {
	env := fake.NewEnv(t)
	r, err := insights.Compute(ctx, env.DB, 30, time.Now(), time.UTC, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Sessions != 0 || r.PullRequests.AcceptanceRate != nil || len(r.Days) != 30 || r.Failures == nil {
		t.Errorf("empty report = %+v", r)
	}
}
