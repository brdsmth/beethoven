package insights_test

import (
	"context"
	"math"
	"slices"
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

	r, err := insights.Compute(ctx, env.DB, insights.Options{Days: 7, Now: now, Loc: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if r.Totals.Sessions != 4 || r.Totals.Settled != 4 {
		t.Fatalf("sessions %d, settled %d; want the 4 people started", r.Totals.Sessions, r.Totals.Settled)
	}
	if tt := r.Totals; !near(tt.FirstTryRate, 0.25) || !near(tt.SuccessRate, 0.5) || !near(tt.Cycles, 1.25) {
		t.Errorf("first try %v, success %v, cycles %v; want 0.25, 0.5, 1.25", tt.FirstTryRate, tt.SuccessRate, tt.Cycles)
	}
	if c := r.CycleCounts; c[0].N != 3 || c[1].N != 1 || c[2].N != 0 || c[3].N != 0 {
		t.Errorf("cycle counts %+v", c)
	}
	if tt := r.Totals; tt.People != 2 || tt.Prompts != 6 || tt.Usage.Output != 200*5 {
		t.Errorf("people %d, prompts %d, usage %+v; want 2, the 6 of people's sessions and their helper, 5 that finished", tt.People, tt.Prompts, tt.Usage)
	}

	pr := r.Totals
	if pr.Opened != 2 || pr.Merged != 1 || pr.Closed != 1 || pr.AcceptanceRate == nil || !near(*pr.AcceptanceRate, 0.5) {
		t.Errorf("pull requests %+v", pr)
	}
	if !near(pr.MedianToMergeSecs, 7200) || !near(pr.P90ToMergeSecs, 7200) {
		t.Errorf("median to merge %v", pr.MedianToMergeSecs)
	}
	if b := r.PullRequests.ToMerge; b[1].Label != "4h" || b[1].N != 1 {
		t.Errorf("to merge %+v", b)
	}
	if g := r.PullRequests.ByRepo; len(g) != 2 || g[0].Name != "acme/app" || g[0].Merged != 1 {
		t.Errorf("by repository %+v", g)
	}
	spent := func(id string) float64 { s, _ := env.DB.Session(ctx, id); return s.CostUSD }
	children, _ := env.DB.Sessions(ctx, store.SessionFilter{ParentID: first.ID})
	if want := spent(first.ID) + spent(children[0].ID); want == 0 || !near(pr.CostPerMerged, want) {
		t.Errorf("cost per merged %v, want the run's %v, its spawned session included", pr.CostPerMerged, want)
	}
	all, _ := env.DB.Sessions(ctx, store.SessionFilter{Limit: 100})
	total, automation := 0.0, 0.0
	for _, s := range all {
		total += s.CostUSD
		if s.AutomationID != "" {
			automation += s.CostUSD
		}
	}
	if !near(r.Totals.Spend, total-automation) {
		t.Errorf("spend %v, want people's %v, their helper included", r.Totals.Spend, total-automation)
	}
	everything, err := insights.Compute(ctx, env.DB, insights.Options{Days: 7, Now: now, Loc: time.UTC, Scope: insights.All})
	if err != nil {
		t.Fatal(err)
	}
	if tt := everything.Totals; !near(tt.Spend, total) || !near(tt.Automations, automation) || tt.Sessions != 6 {
		t.Errorf("every scope: spend %v of %v, automations %v of %v, sessions %d", tt.Spend, total, tt.Automations, automation, tt.Sessions)
	}

	if len(r.Days) != 7 {
		t.Fatalf("%d days", len(r.Days))
	}
	if d := r.Days[6]; d.Sessions != 4 || d.FirstTry != 1 || d.MoreCycles != 1 || d.Unsuccessful != 2 || d.Merged != 1 || d.People != 2 || d.Origins != [5]int{3, 0, 1, 0, 0} {
		t.Errorf("today = %+v", d)
	}
	if len(r.BySource) != 2 || r.BySource[0].Name != "web" || r.BySource[0].Sessions != 3 || r.BySource[1].Name != "slack" {
		t.Errorf("by source %+v", r.BySource)
	}
	if g := r.ByRepo; len(g) != 3 || g[0].Name != "acme/app" || g[0].Merged != 1 || !near(g[0].FirstTryRate, 0.5) {
		t.Errorf("by repository %+v", g)
	}
	if g := r.ByModel; len(g) != 2 || g[0].Name != "claude-opus-5-5" || g[0].Sessions != 3 || g[1].Name != "default" {
		t.Errorf("by model %+v; want the model the agent reported, and default for the failure that reported none", g)
	}
	if p := r.People; len(p) != 2 || p[0].Name != "Jane Dev" || p[0].Sessions != 3 || p[0].Merged != 1 || p[0].Daily[6] != 3 || len(p[0].Recent) != 3 {
		t.Errorf("people %+v", p)
	}
	if run := r.Runs[0]; run.ID != first.ID || run.Children != 1 || run.PR != "merged" {
		t.Errorf("most expensive run %+v; want the first session with its helper", run)
	}
	if e := r.Ended; e.Done != 3 || e.Failed != 1 {
		t.Errorf("ended %+v", e)
	}
	if !slices.Equal(r.Repos, []string{"acme/app", "acme/web"}) {
		t.Errorf("repos %v", r.Repos)
	}
	web, err := insights.Compute(ctx, env.DB, insights.Options{Days: 7, Now: now, Loc: time.UTC, Repo: "acme/web"})
	if err != nil {
		t.Fatal(err)
	}
	if web.Totals.Sessions != 1 || web.Totals.Closed != 1 || len(web.Repos) != 2 {
		t.Errorf("filtered to acme/web: %+v, repos %v", web.Totals, web.Repos)
	}

	// A week later, this window is the one before.
	later, err := insights.Compute(ctx, env.DB, insights.Options{Days: 7, Now: now.AddDate(0, 0, 7), Loc: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if later.Totals.Sessions != 0 || later.Previous.Sessions != 4 || !near(later.Previous.FirstTryRate, 0.25) {
		t.Errorf("a week later: totals %+v, previous %+v", later.Totals, later.Previous)
	}
	if w := later.Weeks; len(w) != 12 || w[10].People != 2 && w[11].People != 2 {
		t.Errorf("weeks %+v", w)
	}

	rel, err := insights.ComputeReliability(ctx, env.DB, insights.Options{Days: 7, Now: now, Loc: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if rel.Prompts != 6 || rel.Failed != 1 || len(rel.Failures) != 1 || rel.Failures[0].Label != "simulated failure" || rel.Failures[0].Daily[6] != 1 {
		t.Errorf("reliability prompts %d, failed %d: %+v", rel.Prompts, rel.Failed, rel.Failures)
	}
	if rel.FailureRate == nil || !near(*rel.FailureRate, 1.0/6) || rel.Duration.N != 6 || rel.QueueWait.N != 6 {
		t.Errorf("failure rate %v, duration %+v, wait %+v", rel.FailureRate, rel.Duration, rel.QueueWait)
	}
	if len(rel.Tools) != 1 || rel.Tools[0].Name != "Bash" || rel.Tools[0].Calls != 6 || rel.ToolErrors != 0 {
		t.Errorf("tools %+v", rel.Tools)
	}
}

func TestNothingYet(t *testing.T) {
	env := fake.NewEnv(t)
	r, err := insights.Compute(ctx, env.DB, insights.Options{Days: 30, Now: time.Now(), Loc: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if r.Totals.Sessions != 0 || r.Totals.AcceptanceRate != nil || r.Totals.CacheHitRate != nil || len(r.Days) != 30 ||
		r.Runs == nil || r.People == nil || r.PullRequests.Waiting == nil || len(r.Weeks) != 12 {
		t.Errorf("empty report = %+v", r)
	}
	rel, err := insights.ComputeReliability(ctx, env.DB, insights.Options{Days: 30, Now: time.Now(), Loc: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if rel.Prompts != 0 || rel.FailureRate != nil || rel.Failures == nil || rel.Tools == nil || len(rel.Days) != 30 {
		t.Errorf("empty reliability = %+v", rel)
	}
}
