// Package insights measures how sessions go: how many cycles a task took,
// how often the first try was enough, what became of the pull requests,
// and who used what, at what cost.
//
// The quality measures cover the sessions people started (not automations,
// nor sessions agents spawned) once they have settled: they ran and have
// nothing queued or running. Each prompt that ran is one cycle. A session
// succeeded when its last cycle finished and none of its pull requests was
// closed without merging; it succeeded on the first try when that took one
// cycle. A session's cost includes the sessions its agent spawned.
package insights

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/brdsmth/beethoven/internal/store"
)

type Report struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`

	Sessions     int     `json:"sessions"` // started by people
	Settled      int     `json:"settled"`
	Cycles       float64 `json:"cycles"`         // per settled session
	CycleCounts  []Count `json:"cycle_counts"`   // settled sessions by cycles: 1, 2, 3, 4+
	FirstTryRate float64 `json:"first_try_rate"` // of settled sessions
	SuccessRate  float64 `json:"success_rate"`

	Prompts      int     `json:"prompts"` // that ran, from any source
	Failed       int     `json:"failed"`
	Stopped      int     `json:"stopped"`
	Failures     []Count `json:"failures"`      // the most common reasons
	AgentSeconds float64 `json:"agent_seconds"` // time agents spent working

	PullRequests PullRequests `json:"pull_requests"`
	Automations  Automations  `json:"automations"`
	Spend        float64      `json:"spend"` // every session, automations and spawned ones included

	Days     []Day   `json:"days"`
	BySource []Group `json:"by_source"`
	ByPerson []Group `json:"by_person"`
	ByRepo   []Group `json:"by_repo"`
}

type Count struct {
	Label string `json:"label"`
	N     int    `json:"n"`
}

// PullRequests describes the pull requests of the window's sessions.
type PullRequests struct {
	Opened             int      `json:"opened"`
	Merged             int      `json:"merged"`
	Closed             int      `json:"closed"` // without merging
	Open               int      `json:"open"`
	AcceptanceRate     *float64 `json:"acceptance_rate"`      // merged ÷ (merged + closed); nil before any is decided
	MedianToOpenSecs   float64  `json:"median_to_open_secs"`  // from the session's start
	MedianToMergeSecs  float64  `json:"median_to_merge_secs"` // from opening
	CostPerMerged      float64  `json:"cost_per_merged"`
	SessionsWithMerged int      `json:"sessions_with_merged"`
}

type Automations struct {
	Runs      int     `json:"runs"` // prompts automations sent that ran
	Succeeded int     `json:"succeeded"`
	Failed    int     `json:"failed"`
	Spend     float64 `json:"spend"`
}

// Day counts the sessions people started on a day, by how they went.
type Day struct {
	Date         string  `json:"date"` // YYYY-MM-DD
	Sessions     int     `json:"sessions"`
	FirstTry     int     `json:"first_try"`
	MoreCycles   int     `json:"more_cycles"`  // succeeded after follow-ups
	Unsuccessful int     `json:"unsuccessful"` // settled without success; the rest are still in progress
	Merged       int     `json:"merged"`       // pull requests merged that day
	Spend        float64 `json:"spend"`
}

type Group struct {
	Name         string  `json:"name"`
	Sessions     int     `json:"sessions"`
	Settled      int     `json:"settled"`
	FirstTryRate float64 `json:"first_try_rate"`
	Cycles       float64 `json:"cycles"`
	Merged       int     `json:"merged"`
	Spend        float64 `json:"spend"`
}

// fact is what a session's prompts and pull requests say about it.
type fact struct {
	s        store.Session
	root     string // the session at the top of its tree of spawned sessions
	cycles   int
	settled  bool
	success  bool
	busy     bool // something queued or running
	merged   int  // pull requests, its spawned sessions' included
	runSpend float64
}

func (f *fact) people() bool { return f.s.ParentID == "" && f.s.AutomationID == "" }

// Compute measures the sessions created in the last days calendar days in
// loc, today included.
func Compute(ctx context.Context, db *store.Store, days int, now time.Time, loc *time.Location) (Report, error) {
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	from, to := today.AddDate(0, 0, 1-days), today.AddDate(0, 0, 1)
	sessions, err := db.SessionsCreated(ctx, from, to)
	if err != nil {
		return Report{}, err
	}
	prompts, err := db.PromptsOfSessionsCreated(ctx, from, to)
	if err != nil {
		return Report{}, err
	}
	pulls, err := db.PullRequestsOf(ctx, from, to)
	if err != nil {
		return Report{}, err
	}
	envs, _ := db.Environments(ctx)
	envNames := map[string]string{}
	for _, e := range envs {
		envNames[e.ID] = e.Name
	}

	r := Report{From: from, To: to, Failures: []Count{}}
	facts := map[string]*fact{}
	var order []*fact
	for _, s := range sessions {
		f := &fact{s: s, root: s.ID}
		if p, ok := facts[s.ParentID]; ok { // a parent is created before its children
			f.root = p.root
		}
		facts[s.ID] = f
		order = append(order, f)
		facts[f.root].runSpend += s.CostUSD
		r.Spend += s.CostUSD
		if s.AutomationID != "" {
			r.Automations.Spend += s.CostUSD
		}
	}

	failures := map[string]int{}
	last := map[string]string{} // the status of each session's latest prompt that ran
	for _, p := range prompts {
		f := facts[p.SessionID]
		if f == nil {
			continue
		}
		if p.Status == store.Queued || p.Status == store.Running {
			f.busy = true
		}
		if p.StartedAt == nil {
			continue // cancelled before it ran
		}
		f.cycles++
		last[p.SessionID] = p.Status
		r.Prompts++
		if p.FinishedAt != nil {
			r.AgentSeconds += p.FinishedAt.Sub(*p.StartedAt).Seconds()
		}
		switch p.Status {
		case store.Failed:
			r.Failed++
			failures[reason(p.Error)]++
		case store.Cancelled:
			r.Stopped++
		}
		if p.AutomationID != "" && p.Status != store.Queued && p.Status != store.Running {
			r.Automations.Runs++
			switch p.Status {
			case store.Done:
				r.Automations.Succeeded++
			case store.Failed:
				r.Automations.Failed++
			}
		}
	}

	pr := &r.PullRequests
	closed := map[string]bool{}
	var toOpen, toMerge []float64
	mergedOn := map[string]int{}
	withMerged := map[string]bool{}
	for _, p := range pulls {
		f := facts[p.SessionID]
		if f == nil {
			continue
		}
		pr.Opened++
		switch p.State {
		case "merged":
			pr.Merged++
			facts[f.root].merged++
			withMerged[f.root] = true
			if p.MergedAt != nil {
				mergedOn[p.MergedAt.In(loc).Format(time.DateOnly)]++
				if !p.OpenedAt.IsZero() {
					toMerge = append(toMerge, p.MergedAt.Sub(p.OpenedAt).Seconds())
				}
			}
		case "closed":
			pr.Closed++
			closed[f.root] = true
		default:
			pr.Open++
		}
		if !p.OpenedAt.IsZero() {
			toOpen = append(toOpen, p.OpenedAt.Sub(facts[f.root].s.CreatedAt).Seconds())
		}
	}
	if decided := pr.Merged + pr.Closed; decided > 0 {
		rate := float64(pr.Merged) / float64(decided)
		pr.AcceptanceRate = &rate
	}
	pr.MedianToOpenSecs, pr.MedianToMergeSecs = median(toOpen), median(toMerge)
	pr.SessionsWithMerged = len(withMerged)
	if pr.Merged > 0 {
		spend := 0.0
		for root := range withMerged {
			spend += facts[root].runSpend
		}
		pr.CostPerMerged = spend / float64(pr.Merged)
	}

	byDate := map[string]*Day{}
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		key := d.Format(time.DateOnly)
		r.Days = append(r.Days, Day{Date: key, Merged: mergedOn[key]})
	}
	for i := range r.Days {
		byDate[r.Days[i].Date] = &r.Days[i]
	}

	counts := make([]int, 4) // 1, 2, 3, 4+ cycles
	cycles, firstTry, success := 0, 0, 0
	sources, people, repos := grouper{}, grouper{}, grouper{}
	for _, f := range order {
		day := byDate[f.s.CreatedAt.In(loc).Format(time.DateOnly)]
		if day != nil {
			day.Spend += f.s.CostUSD
		}
		if !f.people() {
			continue
		}
		f.settled = f.cycles > 0 && !f.busy
		f.success = f.settled && last[f.s.ID] == store.Done && !closed[f.s.ID]
		r.Sessions++
		if day != nil {
			day.Sessions++
		}
		if f.settled {
			r.Settled++
			cycles += f.cycles
			counts[min(f.cycles, 4)-1]++
			switch {
			case f.success && f.cycles == 1:
				firstTry++
				if day != nil {
					day.FirstTry++
				}
			case f.success:
				if day != nil {
					day.MoreCycles++
				}
			default:
				if day != nil {
					day.Unsuccessful++
				}
			}
			if f.success {
				success++
			}
		}
		sources.add(f.s.Source, f)
		people.add(person(f.s.Author), f)
		repos.add(workspace(f.s, envNames), f)
	}
	if r.Settled > 0 {
		r.Cycles = float64(cycles) / float64(r.Settled)
		r.FirstTryRate = float64(firstTry) / float64(r.Settled)
		r.SuccessRate = float64(success) / float64(r.Settled)
	}
	r.CycleCounts = []Count{{"1", counts[0]}, {"2", counts[1]}, {"3", counts[2]}, {"4+", counts[3]}}
	for label, n := range failures {
		r.Failures = append(r.Failures, Count{label, n})
	}
	sort.Slice(r.Failures, func(i, j int) bool {
		a, b := r.Failures[i], r.Failures[j]
		return a.N > b.N || a.N == b.N && a.Label < b.Label
	})
	if len(r.Failures) > 5 {
		r.Failures = r.Failures[:5]
	}
	r.BySource, r.ByPerson, r.ByRepo = sources.groups(), people.groups(), repos.groups()
	return r, nil
}

// grouper accumulates settled sessions into named groups.
type grouper map[string]*acc

type acc struct {
	Group
	cycles, firstTry int
}

func (g grouper) add(name string, f *fact) {
	a := g[name]
	if a == nil {
		a = &acc{Group: Group{Name: name}}
		g[name] = a
	}
	a.Sessions++
	a.Merged += f.merged
	a.Spend += f.runSpend
	if f.settled {
		a.Settled++
		a.cycles += f.cycles
		if f.success && f.cycles == 1 {
			a.firstTry++
		}
	}
}

func (g grouper) groups() []Group {
	out := make([]Group, 0, len(g))
	for _, a := range g {
		if a.Settled > 0 {
			a.Cycles = float64(a.cycles) / float64(a.Settled)
			a.FirstTryRate = float64(a.firstTry) / float64(a.Settled)
		}
		out = append(out, a.Group)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Sessions > out[j].Sessions || out[i].Sessions == out[j].Sessions && out[i].Name < out[j].Name
	})
	return out
}

// reason shortens a failure to what most failures of its kind share: the
// first line, up to the first ": " when that leaves a phrase.
func reason(err string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(err), "\n")
	if head, _, ok := strings.Cut(line, ": "); ok && len(head) >= 8 && len(head) <= 60 {
		line = head
	}
	if r := []rune(line); len(r) > 80 {
		line = string(r[:79]) + "…"
	}
	if line == "" {
		return "unknown"
	}
	return line
}

func person(a store.Author) string {
	if a.Name != "" {
		return a.Name
	}
	if a.Email != "" {
		return a.Email
	}
	return "unknown"
}

// workspace names what a session worked on.
func workspace(s store.Session, envNames map[string]string) string {
	switch {
	case s.EnvironmentID != "":
		if name := envNames[s.EnvironmentID]; name != "" {
			return name + " (environment)"
		}
		return "a deleted environment"
	case len(s.Repos) == 1:
		return s.Repos[0].Repo
	case len(s.Repos) > 1:
		return s.Repos[0].Repo + " and others"
	}
	return "scratch workspace"
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	sort.Float64s(v)
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}
