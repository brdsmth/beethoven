package insights

import (
	"context"
	"sort"
	"time"

	"github.com/brdsmth/beethoven/internal/store"
)

// Reliability measures how well the window's work ran: prompts that failed
// or were stopped and why, how long prompts and sandboxes took, which tools
// failed, and how each automation did. Prompts count like spend does, with
// the session in scope that accounts for them; automations count whatever
// the scope.
type Reliability struct {
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
	Scope string    `json:"scope"`

	Prompts         int      `json:"prompts"` // that ran
	Failed          int      `json:"failed"`
	Stopped         int      `json:"stopped"`
	FailureRate     *float64 `json:"failure_rate"`      // failed ÷ prompts
	PrevFailureRate *float64 `json:"prev_failure_rate"` // over the window before

	Duration     Spread `json:"duration"`      // of a prompt, from starting to finishing
	QueueWait    Spread `json:"queue_wait"`    // from queueing to starting
	SandboxStart Spread `json:"sandbox_start"` // from starting a sandbox to its being ready

	ToolCalls  int    `json:"tool_calls"`
	ToolErrors int    `json:"tool_errors"`
	Tools      []Tool `json:"tools"` // the most called

	Days        []ReliabilityDay `json:"days"`
	Failures    []Failure        `json:"failures"` // the most common reasons
	Automations []Automation     `json:"automations"`
}

// Spread is how long something took, in seconds.
type Spread struct {
	N          int     `json:"n"`
	Median     float64 `json:"median"`
	P90        float64 `json:"p90"`
	PrevMedian float64 `json:"prev_median"` // over the window before; 0 for none
}

type Tool struct {
	Name   string `json:"name"`
	Calls  int    `json:"calls"`
	Errors int    `json:"errors"`
}

type ReliabilityDay struct {
	Date    string `json:"date"`
	Failed  int    `json:"failed"`
	Stopped int    `json:"stopped"`
}

type Failure struct {
	Label     string    `json:"label"`
	N         int       `json:"n"`
	Daily     []int     `json:"daily"`
	LastAt    time.Time `json:"last_at"`
	SessionID string    `json:"session_id"` // where it last happened
}

// Automation is how an automation's runs went in the window.
type Automation struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Trigger    string    `json:"trigger"` // schedule, webhook, manual or an event kind
	Enabled    bool      `json:"enabled"`
	Failures   int       `json:"failures"` // in a row; three pause it
	Runs       int       `json:"runs"`     // prompts it sent that finished
	Succeeded  int       `json:"succeeded"`
	Failed     int       `json:"failed"`
	Merged     int       `json:"merged"`
	Spend      float64   `json:"spend"`
	LastStatus string    `json:"last_status,omitempty"`
	LastAt     time.Time `json:"last_at,omitzero"`
}

// ComputeReliability measures the window opts describe and the one before.
func ComputeReliability(ctx context.Context, db *store.Store, o Options) (Reliability, error) {
	d, err := load(ctx, db, o)
	if err != nil {
		return Reliability{}, err
	}
	o, scope := d.opts, d.opts.scope()
	r := Reliability{From: d.from, To: d.to, Scope: scope, Tools: []Tool{}, Failures: []Failure{}, Automations: []Automation{}}
	index := map[string]int{}
	for day := d.from; day.Before(d.to); day = day.AddDate(0, 0, 1) {
		index[day.Format(time.DateOnly)] = len(r.Days)
		r.Days = append(r.Days, ReliabilityDay{Date: day.Format(time.DateOnly)})
	}

	// Prompts count with the sessions in scope that account for them.
	measure(scope, d.cur)
	measure(scope, d.before)
	var durations, waits [2][]float64 // this window, the one before
	var ran, failed [2]int
	failures := map[string]*Failure{}
	for w, facts := range [][]*fact{d.cur, d.before} {
		for _, f := range facts {
			if f.owner == nil {
				continue
			}
			for _, p := range f.prompts {
				if p.StartedAt == nil {
					continue
				}
				waits[w] = append(waits[w], p.StartedAt.Sub(p.CreatedAt).Seconds())
				if p.FinishedAt == nil {
					continue
				}
				durations[w] = append(durations[w], p.FinishedAt.Sub(*p.StartedAt).Seconds())
				ran[w]++
				if p.Status == store.Failed {
					failed[w]++
				}
				if w == 1 {
					continue
				}
				i, ok := index[p.FinishedAt.In(o.Loc).Format(time.DateOnly)]
				switch p.Status {
				case store.Failed:
					r.Failed++
					if ok {
						r.Days[i].Failed++
					}
					label := reason(p.Error)
					x := failures[label]
					if x == nil {
						x = &Failure{Label: label, Daily: make([]int, len(r.Days))}
						failures[label] = x
					}
					x.N++
					if ok {
						x.Daily[i]++
					}
					if p.FinishedAt.After(x.LastAt) {
						x.LastAt, x.SessionID = *p.FinishedAt, p.SessionID
					}
				case store.Cancelled:
					r.Stopped++
					if ok {
						r.Days[i].Stopped++
					}
				}
			}
		}
	}
	r.Prompts = ran[0]
	r.FailureRate, r.PrevFailureRate = rate(failed[0], ran[0]), rate(failed[1], ran[1])
	r.Duration, r.QueueWait = spread(durations), spread(waits)
	for _, x := range failures {
		r.Failures = append(r.Failures, *x)
	}
	sort.Slice(r.Failures, func(i, j int) bool {
		a, b := r.Failures[i], r.Failures[j]
		return a.N > b.N || a.N == b.N && a.Label < b.Label
	})
	r.Failures = r.Failures[:min(len(r.Failures), 8)]

	// Sandboxes and tools, the same way.
	window := map[string]int{} // session → 0 this window, 1 the one before
	for w, facts := range [][]*fact{d.cur, d.before} {
		for _, f := range facts {
			if f.owner != nil {
				window[f.s.ID] = w
			}
		}
	}
	states, err := db.SandboxStatesOfSessionsCreated(ctx, d.prev, d.to)
	if err != nil {
		return r, err
	}
	var boots [2][]float64
	var starting time.Time
	session := ""
	for _, x := range states {
		w, ok := window[x.SessionID]
		if !ok {
			continue
		}
		if x.SessionID != session {
			session, starting = x.SessionID, time.Time{}
		}
		switch x.State {
		case "starting":
			starting = x.At
		case "ready":
			if !starting.IsZero() {
				boots[w] = append(boots[w], x.At.Sub(starting).Seconds())
				starting = time.Time{}
			}
		default:
			starting = time.Time{}
		}
	}
	r.SandboxStart = spread(boots)
	uses, err := db.ToolUseOfSessionsCreated(ctx, d.from, d.to)
	if err != nil {
		return r, err
	}
	tools := map[string]*Tool{}
	for _, u := range uses {
		if w, ok := window[u.SessionID]; !ok || w != 0 {
			continue
		}
		t := tools[u.Name]
		if t == nil {
			t = &Tool{Name: u.Name}
			tools[u.Name] = t
		}
		t.Calls += u.Calls
		t.Errors += u.Errors
		r.ToolCalls += u.Calls
		r.ToolErrors += u.Errors
	}
	for _, t := range tools {
		r.Tools = append(r.Tools, *t)
	}
	sort.Slice(r.Tools, func(i, j int) bool {
		a, b := r.Tools[i], r.Tools[j]
		return a.Calls > b.Calls || a.Calls == b.Calls && a.Name < b.Name
	})
	r.Tools = r.Tools[:min(len(r.Tools), 12)]

	// Automations, whatever the scope.
	list, err := db.Automations(ctx)
	if err != nil {
		return r, err
	}
	health := map[string]*Automation{}
	for _, a := range list {
		if o.Team != "" && a.TeamID != trimWorkspace(o.Team) {
			continue
		}
		health[a.ID] = &Automation{ID: a.ID, Name: a.Name, Trigger: trigger(a), Enabled: a.Enabled, Failures: a.Failures}
	}
	measure(All, d.cur) // every session accounts for itself; tops pay for their trees
	for _, f := range d.cur {
		if a := health[f.top.s.AutomationID]; a != nil {
			a.Spend += f.s.CostUSD
			for _, p := range f.pulls {
				if p.State == "merged" {
					a.Merged++
				}
			}
		}
		for _, p := range f.prompts {
			a := health[p.AutomationID]
			if a == nil || p.FinishedAt == nil || p.StartedAt == nil {
				continue
			}
			a.Runs++
			switch p.Status {
			case store.Done:
				a.Succeeded++
			case store.Failed:
				a.Failed++
			}
			if p.FinishedAt.After(a.LastAt) {
				a.LastAt, a.LastStatus = *p.FinishedAt, p.Status
			}
		}
	}
	for _, a := range health {
		if a.Runs > 0 || a.Enabled {
			r.Automations = append(r.Automations, *a)
		}
	}
	sort.Slice(r.Automations, func(i, j int) bool {
		a, b := r.Automations[i], r.Automations[j]
		return a.Runs > b.Runs || a.Runs == b.Runs && a.Name < b.Name
	})
	return r, nil
}

func spread(v [2][]float64) Spread {
	return Spread{N: len(v[0]), Median: quantile(v[0], .5), P90: quantile(v[0], .9), PrevMedian: quantile(v[1], .5)}
}

func trigger(a store.Automation) string {
	switch {
	case a.TriggerEvent != "":
		return a.TriggerEvent
	case a.Schedule != "":
		return "schedule"
	case a.HookHash != "":
		return "webhook"
	}
	return "manual"
}

func trimWorkspace(team string) string {
	if team == "workspace" {
		return ""
	}
	return team
}
