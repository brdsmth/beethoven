// Package insights measures how sessions go: how many cycles a task took,
// how often the first try was enough, what became of the pull requests,
// who used what, at what cost, and how reliably it all ran.
//
// Measures cover the sessions created in a window of calendar days, and
// the same measures over the window before, for comparison. A scope picks
// the sessions they cover: the ones people started (the default), the ones
// automations started, the ones agents spawned, or all. The work of a
// session outside the scope (its spend, prompts, tokens and pull requests)
// counts for the nearest session above it that is in scope: a person's
// session pays for the helpers its agent spawned. Pull requests count from
// every source, whatever the scope.
//
// The quality measures cover sessions once they have settled: they ran and
// have nothing queued or running. Each prompt that ran is one cycle. A
// session succeeded when its last cycle finished and none of its pull
// requests was closed without merging; it succeeded on the first try when
// that took one cycle.
package insights

import (
	"context"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/brdsmth/beethoven/internal/store"
)

// Scopes.
const (
	People      = "people"
	Automations = "automations"
	Agents      = "agents"
	All         = "all"
)

// ValidScope reports whether s names a scope.
func ValidScope(s string) bool { return s == People || s == Automations || s == Agents || s == All }

// Options say what to measure.
type Options struct {
	Days  int // calendar days in loc, today included
	Now   time.Time
	Loc   *time.Location
	Scope string // "" = People
	Repo  string // only sessions working on this repository; "" = any
	Team  string // the team the sessions are filtered to, for invitations: "" = every team, "workspace" = none
	// Keep says which sessions count: those the viewer may see, of the
	// chosen team. nil keeps every session.
	Keep func(store.Session) bool
}

type Report struct {
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
	Scope string    `json:"scope"`
	Repos []string  `json:"repos"` // what the window's sessions worked on, to filter by

	Totals   Totals `json:"totals"`
	Previous Totals `json:"previous"` // over the window before, as long

	CycleCounts     []Count    `json:"cycle_counts"` // settled sessions by cycles: 1, 2, 3, 4+
	Days            []Day      `json:"days"`
	Sources         []Count    `json:"sources"` // sessions by origin: web, cli, slack, automation, agent
	Ended           Ended      `json:"ended"`
	Hours           [7][24]int `json:"hours"` // sessions by weekday, Monday first, and hour, in loc
	ChildrenPerRoot float64    `json:"children_per_root"`

	ByRepo        []Group `json:"by_repo"`
	ByProject     []Group `json:"by_project"`
	ByTeam        []Group `json:"by_team"`
	ByModel       []Group `json:"by_model"`
	ByModelEffort []Group `json:"by_model_effort"`
	BySource      []Group `json:"by_source"`
	Runs          []Run   `json:"runs"` // the most expensive

	PullRequests PullRequests `json:"pull_requests"`
	People       []Person     `json:"people"`
	Weeks        []Week       `json:"weeks"` // the last twelve, this one included
	Funnel       Funnel       `json:"funnel"`
}

// Totals are a window's headline measures.
type Totals struct {
	Sessions     int     `json:"sessions"`
	Settled      int     `json:"settled"`
	Cycles       float64 `json:"cycles"`         // per settled session
	FirstTryRate float64 `json:"first_try_rate"` // of settled sessions
	SuccessRate  float64 `json:"success_rate"`
	People       int     `json:"people"` // who started a session

	Prompts      int         `json:"prompts"`       // that ran
	AgentSeconds float64     `json:"agent_seconds"` // time agents spent working
	Spend        float64     `json:"spend"`
	PerSession   float64     `json:"per_session"`
	Automations  float64     `json:"automation_spend"` // of Spend, by automations' sessions
	Usage        store.Usage `json:"usage"`
	CacheHitRate *float64    `json:"cache_hit_rate"` // cache reads ÷ (cache reads + input); nil without token data

	// Pull requests, from every source.
	Opened            int      `json:"opened"`
	Merged            int      `json:"merged"`
	Closed            int      `json:"closed"` // without merging
	Open              int      `json:"open"`
	AcceptanceRate    *float64 `json:"acceptance_rate"`      // merged ÷ (merged + closed); nil before any is decided
	MedianToOpenSecs  float64  `json:"median_to_open_secs"`  // from the session's start
	MedianToMergeSecs float64  `json:"median_to_merge_secs"` // from opening
	P90ToMergeSecs    float64  `json:"p90_to_merge_secs"`
	CostPerMerged     float64  `json:"cost_per_merged"` // what the runs that merged one cost, per merged
}

type Count struct {
	Label string `json:"label"`
	N     int    `json:"n"`
}

// Day measures the sessions created on a day.
type Day struct {
	Date         string     `json:"date"` // YYYY-MM-DD
	Sessions     int        `json:"sessions"`
	Settled      int        `json:"settled"`
	Cycles       int        `json:"cycles"` // of the settled ones
	FirstTry     int        `json:"first_try"`
	MoreCycles   int        `json:"more_cycles"`  // succeeded after follow-ups
	Unsuccessful int        `json:"unsuccessful"` // settled without success; the rest are in progress
	People       int        `json:"people"`
	Origins      [5]int     `json:"origins"` // sessions by origin, in the order of Origins
	Opened       int        `json:"opened"`  // pull requests opened that day
	Merged       int        `json:"merged"`  // merged that day
	Spend        float64    `json:"spend"`
	SpendBy      [3]float64 `json:"spend_by"` // people's sessions, automations', agents'
}

// Origins name where sessions start, in Day.Origins order.
var Origins = []string{"web", "cli", "slack", "automation", "agent"}

// Ended counts the window's sessions by status.
type Ended struct {
	Done     int `json:"done"`
	Failed   int `json:"failed"`
	Stopped  int `json:"stopped"`
	Active   int `json:"active"` // idle or running
	Archived int `json:"archived"`
}

type Group struct {
	Name          string      `json:"name"`
	Sessions      int         `json:"sessions"`
	Settled       int         `json:"settled"`
	FirstTryRate  float64     `json:"first_try_rate"`
	Cycles        float64     `json:"cycles"`
	Prompts       int         `json:"prompts"`
	AgentSeconds  float64     `json:"agent_seconds"`
	Merged        int         `json:"merged"`
	Spend         float64     `json:"spend"`
	CostPerMerged float64     `json:"cost_per_merged"`
	Usage         store.Usage `json:"usage"`
	PrevSpend     *float64    `json:"prev_spend,omitempty"` // over the window before; teams only
}

// Run is a session in scope with the work it accounts for.
type Run struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Person       string    `json:"person"`
	CreatedAt    time.Time `json:"created_at"`
	Children     int       `json:"children"` // sessions out of scope it accounts for
	Cycles       int       `json:"cycles"`
	AgentSeconds float64   `json:"agent_seconds"`
	Status       string    `json:"status"`
	PR           string    `json:"pr"` // its pull requests' best state: merged, open, closed or ""
	Spend        float64   `json:"spend"`
}

// PullRequests describes the pull requests of the window's sessions, and
// those open now.
type PullRequests struct {
	Draft    int       `json:"draft"`     // of the open ones
	ToMerge  []Count   `json:"to_merge"`  // merged ones by time to merge: 1h, 4h, 24h, 3d, 7d, more
	BySource []PRGroup `json:"by_source"` // by the origin of the session at the top
	ByRepo   []PRGroup `json:"by_repo"`

	OpenNow        int      `json:"open_now"` // whenever opened
	OldestOpenSecs float64  `json:"oldest_open_secs"`
	Waiting        []OpenPR `json:"waiting"` // the oldest open ones
}

type PRGroup struct {
	Name              string   `json:"name"`
	Opened            int      `json:"opened"`
	Merged            int      `json:"merged"`
	Closed            int      `json:"closed"`
	AcceptanceRate    *float64 `json:"acceptance_rate"`
	MedianToMergeSecs float64  `json:"median_to_merge_secs"`
	CostPerMerged     float64  `json:"cost_per_merged"`
}

type OpenPR struct {
	Repo      string    `json:"repo"`
	Number    int       `json:"number"`
	URL       string    `json:"url"`
	Draft     bool      `json:"draft"`
	OpenedAt  time.Time `json:"opened_at"`
	SessionID string    `json:"session_id"`
	Title     string    `json:"title"`
	Person    string    `json:"person"`
}

// Person is someone's sessions in scope.
type Person struct {
	Group
	AccountID  string    `json:"account_id,omitempty"`
	LastActive time.Time `json:"last_active"`
	Daily      []int     `json:"daily"` // sessions per day of the window
	Repos      []Count   `json:"repos"` // what they worked on most
	Recent     []Run     `json:"recent"`
}

type Week struct {
	Start  string `json:"start"` // its Monday, YYYY-MM-DD
	People int    `json:"people"`
}

// Funnel follows the people invited in the last 90 days.
type Funnel struct {
	Invited           int     `json:"invited"`
	Joined            int     `json:"joined"`
	Started           int     `json:"started"` // had a first session
	Merged            int     `json:"merged"`  // had a pull request merged
	MedianToStartSecs float64 `json:"median_to_start_secs"`
}

// fact is what a session's prompts and pull requests say about it.
type fact struct {
	s       store.Session
	parent  *fact
	prompts []store.Prompt
	pulls   []store.PullRequest

	cycles  int
	ran     int // prompts that ran, the same as cycles
	seconds float64
	usage   store.Usage
	busy    bool   // something queued or running
	last    string // the status of its latest prompt that ran
	model   string // what did most of its work

	// Set per window by measure.
	owner, top                 *fact // the nearest session above or itself in scope; the topmost in the window
	settled, success           bool
	spend, treeSpend, workSecs float64 // accounted for: as owner, as top; its owned agent time
	work, children, merged     int     // prompts and sessions it accounts for, pull requests merged
	workUsage                  store.Usage
	pr                         string // best pull request state it accounts for
	closed                     bool   // as top: a pull request of its tree was closed unmerged
}

// data is what a report is computed from.
type data struct {
	opts                 Options
	from, to, prev, week time.Time // the window, the one before and the first of the twelve weeks
	cur, before          []*fact
	weekly               []store.Session // since week, kept and in scope
	repos                []string
	envNames, teamNames  map[string]string
}

func (o Options) scope() string {
	if o.Scope == "" {
		return People
	}
	return o.Scope
}

func inScope(scope string, s store.Session) bool {
	switch scope {
	case Automations:
		return s.ParentID == "" && s.AutomationID != ""
	case Agents:
		return s.ParentID != ""
	case All:
		return true
	}
	return s.ParentID == "" && s.AutomationID == ""
}

func hasRepo(s store.Session, repo string) bool {
	return slices.ContainsFunc(s.Repos, func(r store.RepoRef) bool { return r.Repo == repo })
}

func load(ctx context.Context, db *store.Store, o Options) (*data, error) {
	if o.Loc == nil {
		o.Loc = time.UTC
	}
	local := o.Now.In(o.Loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, o.Loc)
	d := &data{opts: o, to: today.AddDate(0, 0, 1)}
	d.from = today.AddDate(0, 0, 1-o.Days)
	d.prev = d.from.AddDate(0, 0, -o.Days)
	d.week = today.AddDate(0, 0, -(int(today.Weekday())+6)%7-7*11)
	start := d.prev
	if d.week.Before(start) {
		start = d.week
	}
	all, err := db.SessionsCreated(ctx, start, d.to)
	if err != nil {
		return nil, err
	}
	prompts, err := db.PromptsOfSessionsCreated(ctx, d.prev, d.to)
	if err != nil {
		return nil, err
	}
	pulls, err := db.PullRequestsOf(ctx, d.prev, d.to)
	if err != nil {
		return nil, err
	}
	envs, _ := db.Environments(ctx)
	d.envNames = map[string]string{}
	for _, e := range envs {
		d.envNames[e.ID] = e.Name
	}
	teams, _ := db.Teams(ctx)
	d.teamNames = map[string]string{}
	for _, t := range teams {
		d.teamNames[t.ID] = t.Name
	}

	// Only the sessions Keep accepts count: what the viewer may see. A
	// prompt or pull request counts with its session.
	scope, repos := o.scope(), map[string]bool{}
	facts := map[string]*fact{}
	for _, s := range all {
		if o.Keep != nil && !o.Keep(s) {
			continue
		}
		if !s.CreatedAt.Before(d.from) {
			for _, r := range s.Repos {
				repos[r.Repo] = true
			}
		}
		if o.Repo != "" && !hasRepo(s, o.Repo) {
			continue
		}
		if !s.CreatedAt.Before(d.week) && inScope(scope, s) {
			d.weekly = append(d.weekly, s)
		}
		if s.CreatedAt.Before(d.prev) {
			continue
		}
		f := &fact{s: s, parent: facts[s.ParentID]} // a parent is created before its children
		facts[s.ID] = f
		if s.CreatedAt.Before(d.from) {
			d.before = append(d.before, f)
		} else {
			d.cur = append(d.cur, f)
		}
	}
	for r := range repos {
		d.repos = append(d.repos, r)
	}
	sort.Strings(d.repos)

	for _, p := range prompts {
		if f := facts[p.SessionID]; f != nil {
			f.prompts = append(f.prompts, p)
		}
	}
	for _, p := range pulls {
		if f := facts[p.SessionID]; f != nil {
			f.pulls = append(f.pulls, p)
		}
	}
	for _, f := range facts {
		models := map[string]float64{}
		for _, p := range f.prompts {
			if p.Status == store.Queued || p.Status == store.Running {
				f.busy = true
			}
			if p.StartedAt == nil {
				continue // cancelled before it ran
			}
			f.cycles++
			f.ran++
			f.last = p.Status
			if p.FinishedAt != nil {
				f.seconds += p.FinishedAt.Sub(*p.StartedAt).Seconds()
			}
			f.usage.Add(p.Usage)
			if p.AgentModel != "" {
				models[p.AgentModel] += p.CostUSD + 1e-9 // free turns still say which model ran
			}
		}
		f.model = firstOf(f.s.Model, "default")
		best := 0.0
		for m, c := range models {
			if c > best || c == best && m < f.model {
				f.model, best = m, c
			}
		}
	}
	return d, nil
}

// measure works out the window's totals and what each session accounts for.
func measure(scope string, facts []*fact) Totals {
	var t Totals
	in := map[*fact]bool{}
	for _, f := range facts {
		in[f] = true
	}
	people := map[string]bool{}
	cycles, firstTry, success := 0, 0, 0
	for _, f := range facts {
		f.owner, f.top = nil, f
		f.spend, f.treeSpend, f.workSecs, f.work, f.children, f.merged, f.workUsage, f.pr, f.closed = 0, 0, 0, 0, 0, 0, store.Usage{}, "", false
		for g := f; g != nil && in[g]; g = g.parent {
			if f.owner == nil && inScope(scope, g.s) {
				f.owner = g
			}
			f.top = g
		}
		f.top.treeSpend += f.s.CostUSD
		if o := f.owner; o != nil {
			o.spend += f.s.CostUSD
			o.work += f.ran
			o.workSecs += f.seconds
			o.workUsage.Add(f.usage)
			if o != f {
				o.children++
			}
			t.Spend += f.s.CostUSD
			t.Prompts += f.ran
			t.AgentSeconds += f.seconds
			t.Usage.Add(f.usage)
			if f.s.AutomationID != "" || o.s.AutomationID != "" {
				t.Automations += f.s.CostUSD
			}
		}
	}

	var toOpen, toMerge []float64
	withMerged := map[*fact]bool{}
	for _, f := range facts {
		for _, p := range f.pulls {
			t.Opened++
			if o := f.owner; o != nil {
				o.pr = better(o.pr, p.State)
			}
			switch p.State {
			case "merged":
				t.Merged++
				if f.owner != nil {
					f.owner.merged++
				}
				withMerged[f.top] = true
				if p.MergedAt != nil && !p.OpenedAt.IsZero() {
					toMerge = append(toMerge, p.MergedAt.Sub(p.OpenedAt).Seconds())
				}
			case "closed":
				t.Closed++
				f.top.closed = true
			default:
				t.Open++
			}
			if !p.OpenedAt.IsZero() {
				toOpen = append(toOpen, p.OpenedAt.Sub(f.top.s.CreatedAt).Seconds())
			}
		}
	}
	t.AcceptanceRate = rate(t.Merged, t.Merged+t.Closed)
	t.MedianToOpenSecs, t.MedianToMergeSecs, t.P90ToMergeSecs = quantile(toOpen, .5), quantile(toMerge, .5), quantile(toMerge, .9)
	if t.Merged > 0 {
		spend := 0.0
		for top := range withMerged {
			spend += top.treeSpend
		}
		t.CostPerMerged = spend / float64(t.Merged)
	}

	for _, f := range facts {
		if !inScope(scope, f.s) {
			continue
		}
		t.Sessions++
		people[personKey(f.s)] = true
		f.settled = f.cycles > 0 && !f.busy
		f.success = f.settled && f.last == store.Done && !f.top.closed
		if f.settled {
			t.Settled++
			cycles += f.cycles
			if f.success {
				success++
				if f.cycles == 1 {
					firstTry++
				}
			}
		}
	}
	t.People = len(people)
	if t.Settled > 0 {
		t.Cycles = float64(cycles) / float64(t.Settled)
		t.FirstTryRate = float64(firstTry) / float64(t.Settled)
		t.SuccessRate = float64(success) / float64(t.Settled)
	}
	if t.Sessions > 0 {
		t.PerSession = t.Spend / float64(t.Sessions)
	}
	if read := t.Usage.CacheRead; read+t.Usage.Input > 0 {
		t.CacheHitRate = rate64(read, read+t.Usage.Input)
	}
	return t
}

// Compute measures the sessions created in the last opts.Days calendar
// days, today included, and the days before for comparison.
func Compute(ctx context.Context, db *store.Store, o Options) (Report, error) {
	d, err := load(ctx, db, o)
	if err != nil {
		return Report{}, err
	}
	o, scope := d.opts, d.opts.scope()
	loc := o.Loc
	r := Report{From: d.from, To: d.to, Scope: scope, Repos: d.repos}
	if r.Repos == nil {
		r.Repos = []string{}
	}
	r.Previous = measure(scope, d.before)
	prevTeams := map[string]float64{}
	for _, f := range d.before {
		if f.owner == f {
			prevTeams[teamName(f.s, d.teamNames)] += f.spend
		}
	}
	r.Totals = measure(scope, d.cur)

	byDate := map[string]*Day{}
	for day := d.from; day.Before(d.to); day = day.AddDate(0, 0, 1) {
		r.Days = append(r.Days, Day{Date: day.Format(time.DateOnly)})
	}
	for i := range r.Days {
		byDate[r.Days[i].Date] = &r.Days[i]
	}
	dayOf := func(t time.Time) *Day { return byDate[t.In(loc).Format(time.DateOnly)] }
	index := map[string]int{}
	for i, day := range r.Days {
		index[day.Date] = i
	}

	pr := &r.PullRequests
	toMerge := make([]int, 6)
	bySource, byRepo := prGrouper{}, prGrouper{}
	for _, f := range d.cur {
		for _, p := range f.pulls {
			if day := dayOf(p.OpenedAt); day != nil && !p.OpenedAt.IsZero() {
				day.Opened++
			}
			if p.State == "open" && p.Draft {
				pr.Draft++
			}
			var secs float64
			if p.State == "merged" && p.MergedAt != nil {
				if day := dayOf(*p.MergedAt); day != nil {
					day.Merged++
				}
				if !p.OpenedAt.IsZero() {
					secs = p.MergedAt.Sub(p.OpenedAt).Seconds()
					toMerge[bucket(secs)]++
				}
			}
			bySource.add(origin(f.top.s), p, secs, f.top)
			byRepo.add(p.Repo, p, secs, f.top)
		}
	}
	pr.ToMerge = []Count{{"1h", toMerge[0]}, {"4h", toMerge[1]}, {"24h", toMerge[2]}, {"3d", toMerge[3]}, {"7d", toMerge[4]}, {"more", toMerge[5]}}
	pr.BySource, pr.ByRepo = bySource.groups(), byRepo.groups()
	if err := waiting(ctx, db, d, pr); err != nil {
		return Report{}, err
	}

	counts := make([]int, 4) // 1, 2, 3, 4+ cycles
	origins := map[string]int{}
	dayPeople := map[*Day]map[string]bool{}
	roots, children := 0, 0
	repos, projects, teams, models, efforts, sources := grouper{}, grouper{}, grouper{}, grouper{}, grouper{}, grouper{}
	people := map[string]*personAcc{}
	var runs []*fact
	for _, f := range d.cur {
		day := dayOf(f.s.CreatedAt)
		if f.owner != nil && day != nil {
			day.Spend += f.s.CostUSD
			day.SpendBy[spendKind(f.s)] += f.s.CostUSD
		}
		if !inScope(scope, f.s) {
			continue
		}
		if f.s.ParentID == "" {
			roots++
		}
		children += f.children
		key := personKey(f.s)
		o := origin(f.s)
		origins[o]++
		if day != nil {
			day.Sessions++
			day.Origins[slices.Index(Origins, o)]++
			if dayPeople[day] == nil {
				dayPeople[day] = map[string]bool{}
			}
			dayPeople[day][key] = true
			if f.settled {
				day.Settled++
				day.Cycles += f.cycles
				switch {
				case f.success && f.cycles == 1:
					day.FirstTry++
				case f.success:
					day.MoreCycles++
				default:
					day.Unsuccessful++
				}
			}
		}
		if f.settled {
			counts[min(f.cycles, 4)-1]++
		}
		switch {
		case f.s.Status == "done":
			r.Ended.Done++
		case f.s.Status == "failed":
			r.Ended.Failed++
		case f.s.Status == "stopped":
			r.Ended.Stopped++
		default:
			r.Ended.Active++
		}
		if f.s.Archived {
			r.Ended.Archived++
		}
		local := f.s.CreatedAt.In(loc)
		r.Hours[(int(local.Weekday())+6)%7][local.Hour()]++

		repos.add(primary(f.s), f)
		projects.add(project(f.s, d.envNames), f)
		teams.add(teamName(f.s, d.teamNames), f)
		models.add(f.model, f)
		efforts.add(f.model+" · "+firstOf(f.s.Effort, "default"), f)
		sources.add(o, f)
		p := people[key]
		if p == nil {
			p = &personAcc{acc: acc{Group: Group{Name: personName(f.s)}}, id: f.s.AccountID, daily: make([]int, len(r.Days)), repos: map[string]int{}}
			people[key] = p
		}
		p.add(f)
		if f.s.CreatedAt.After(p.last) {
			p.last = f.s.CreatedAt
		}
		if i, ok := index[local.Format(time.DateOnly)]; ok {
			p.daily[i]++
		}
		p.repos[primary(f.s)]++
		p.recent = append(p.recent, f)
		runs = append(runs, f)
	}
	for day, set := range dayPeople {
		day.People = len(set)
	}
	if roots > 0 {
		r.ChildrenPerRoot = float64(children) / float64(roots)
	}
	r.CycleCounts = []Count{{"1", counts[0]}, {"2", counts[1]}, {"3", counts[2]}, {"4+", counts[3]}}
	r.Sources = []Count{}
	for _, o := range Origins {
		if origins[o] > 0 {
			r.Sources = append(r.Sources, Count{o, origins[o]})
		}
	}
	sort.SliceStable(r.Sources, func(i, j int) bool { return r.Sources[i].N > r.Sources[j].N })
	r.ByRepo, r.ByProject, r.ByModel, r.ByModelEffort, r.BySource = repos.groups(), projects.groups(), models.groups(), efforts.groups(), sources.groups()
	r.ByTeam = teams.groups()
	for i := range r.ByTeam {
		prev := prevTeams[r.ByTeam[i].Name]
		r.ByTeam[i].PrevSpend = &prev
	}
	sortBySpend(r.ByModel)
	sortBySpend(r.ByTeam)

	sort.SliceStable(runs, func(i, j int) bool { return runs[i].spend > runs[j].spend })
	r.Runs = []Run{}
	for _, f := range runs[:min(len(runs), 10)] {
		r.Runs = append(r.Runs, run(f))
	}

	r.People = []Person{}
	for _, p := range people {
		r.People = append(r.People, p.person())
	}
	sort.Slice(r.People, func(i, j int) bool {
		a, b := r.People[i], r.People[j]
		return a.Sessions > b.Sessions || a.Sessions == b.Sessions && a.Name < b.Name
	})

	weeks := map[string]map[string]bool{}
	for w := d.week; w.Before(d.to); w = w.AddDate(0, 0, 7) {
		key := w.Format(time.DateOnly)
		weeks[key] = map[string]bool{}
		r.Weeks = append(r.Weeks, Week{Start: key})
	}
	for _, s := range d.weekly {
		local := s.CreatedAt.In(loc)
		monday := time.Date(local.Year(), local.Month(), local.Day()-(int(local.Weekday())+6)%7, 0, 0, 0, 0, loc)
		if set := weeks[monday.Format(time.DateOnly)]; set != nil {
			set[personKey(s)] = true
		}
	}
	for i := range r.Weeks {
		r.Weeks[i].People = len(weeks[r.Weeks[i].Start])
	}

	r.Funnel, err = funnel(ctx, db, o)
	return r, err
}

// waiting fills in the pull requests open now, whenever their sessions
// started.
func waiting(ctx context.Context, db *store.Store, d *data, pr *PullRequests) error {
	open, err := db.OpenPullRequests(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(open))
	for _, p := range open {
		ids = append(ids, p.SessionID)
	}
	sessions, err := db.SessionsByID(ctx, ids)
	if err != nil {
		return err
	}
	byID := map[string]store.Session{}
	for _, s := range sessions {
		if (d.opts.Keep == nil || d.opts.Keep(s)) && (d.opts.Repo == "" || hasRepo(s, d.opts.Repo)) {
			byID[s.ID] = s
		}
	}
	pr.Waiting = []OpenPR{}
	for _, p := range open {
		s, ok := byID[p.SessionID]
		if !ok {
			continue
		}
		pr.OpenNow++
		opened := p.OpenedAt
		if opened.IsZero() {
			opened = s.CreatedAt
		}
		pr.Waiting = append(pr.Waiting, OpenPR{Repo: p.Repo, Number: p.Number, URL: p.URL, Draft: p.Draft, OpenedAt: opened,
			SessionID: s.ID, Title: s.Title, Person: personName(s)})
	}
	sort.Slice(pr.Waiting, func(i, j int) bool { return pr.Waiting[i].OpenedAt.Before(pr.Waiting[j].OpenedAt) })
	if len(pr.Waiting) > 0 {
		pr.OldestOpenSecs = d.opts.Now.Sub(pr.Waiting[0].OpenedAt).Seconds()
	}
	pr.Waiting = pr.Waiting[:min(len(pr.Waiting), 10)]
	return nil
}

// funnel follows the people invited in the last 90 days, to the team the
// report is filtered to.
func funnel(ctx context.Context, db *store.Store, o Options) (Funnel, error) {
	invites, err := db.InviteOutcomes(ctx, o.Now.AddDate(0, 0, -90))
	if err != nil {
		return Funnel{}, err
	}
	var f Funnel
	var toStart []float64
	for _, in := range invites {
		if o.Team != "" && in.TeamID != trimWorkspace(o.Team) {
			continue
		}
		f.Invited++
		if in.UsedAt == nil {
			continue
		}
		f.Joined++
		if in.FirstSession != nil {
			f.Started++
			toStart = append(toStart, math.Max(0, in.FirstSession.Sub(*in.UsedAt).Seconds()))
		}
		if in.Merged {
			f.Merged++
		}
	}
	f.MedianToStartSecs = quantile(toStart, .5)
	return f, nil
}

func run(f *fact) Run {
	return Run{ID: f.s.ID, Title: f.s.Title, Person: personName(f.s), CreatedAt: f.s.CreatedAt, Children: f.children,
		Cycles: f.cycles, AgentSeconds: f.workSecs, Status: f.s.Status, PR: f.pr, Spend: f.spend}
}

// grouper accumulates sessions in scope into named groups.
type grouper map[string]*acc

type acc struct {
	Group
	cycles, firstTry int
	mergedSpend      float64
}

func (a *acc) add(f *fact) {
	a.Sessions++
	a.Merged += f.merged
	a.Spend += f.spend
	a.Prompts += f.work
	a.AgentSeconds += f.workSecs
	a.Usage.Add(f.workUsage)
	if f.merged > 0 {
		a.mergedSpend += f.spend
	}
	if f.settled {
		a.Settled++
		a.cycles += f.cycles
		if f.success && f.cycles == 1 {
			a.firstTry++
		}
	}
}

func (a *acc) group() Group {
	if a.Settled > 0 {
		a.Cycles = float64(a.cycles) / float64(a.Settled)
		a.FirstTryRate = float64(a.firstTry) / float64(a.Settled)
	}
	if a.Merged > 0 {
		a.CostPerMerged = a.mergedSpend / float64(a.Merged)
	}
	return a.Group
}

func (g grouper) add(name string, f *fact) {
	a := g[name]
	if a == nil {
		a = &acc{Group: Group{Name: name}}
		g[name] = a
	}
	a.add(f)
}

func (g grouper) groups() []Group {
	out := make([]Group, 0, len(g))
	for _, a := range g {
		out = append(out, a.group())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Sessions > out[j].Sessions || out[i].Sessions == out[j].Sessions && out[i].Name < out[j].Name
	})
	return out
}

func sortBySpend(g []Group) {
	sort.SliceStable(g, func(i, j int) bool { return g[i].Spend > g[j].Spend })
}

type personAcc struct {
	acc
	id     string
	last   time.Time
	daily  []int
	repos  map[string]int
	recent []*fact
}

func (p *personAcc) person() Person {
	out := Person{Group: p.group(), AccountID: p.id, LastActive: p.last, Daily: p.daily, Repos: []Count{}, Recent: []Run{}}
	for name, n := range p.repos {
		out.Repos = append(out.Repos, Count{name, n})
	}
	sort.Slice(out.Repos, func(i, j int) bool {
		a, b := out.Repos[i], out.Repos[j]
		return a.N > b.N || a.N == b.N && a.Label < b.Label
	})
	out.Repos = out.Repos[:min(len(out.Repos), 3)]
	for i := len(p.recent) - 1; i >= 0 && len(out.Recent) < 5; i-- {
		out.Recent = append(out.Recent, run(p.recent[i]))
	}
	return out
}

// prGrouper accumulates pull requests into named groups.
type prGrouper map[string]*prAcc

type prAcc struct {
	PRGroup
	toMerge []float64
	tops    map[*fact]bool // the runs that merged one
}

func (g prGrouper) add(name string, p store.PullRequest, toMerge float64, top *fact) {
	a := g[name]
	if a == nil {
		a = &prAcc{PRGroup: PRGroup{Name: name}, tops: map[*fact]bool{}}
		g[name] = a
	}
	a.Opened++
	switch p.State {
	case "merged":
		a.Merged++
		a.tops[top] = true
		if toMerge > 0 {
			a.toMerge = append(a.toMerge, toMerge)
		}
	case "closed":
		a.Closed++
	}
}

func (g prGrouper) groups() []PRGroup {
	out := make([]PRGroup, 0, len(g))
	for _, a := range g {
		a.AcceptanceRate = rate(a.Merged, a.Merged+a.Closed)
		a.MedianToMergeSecs = quantile(a.toMerge, .5)
		if a.Merged > 0 {
			spend := 0.0
			for top := range a.tops {
				spend += top.treeSpend
			}
			a.CostPerMerged = spend / float64(a.Merged)
		}
		out = append(out, a.PRGroup)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Opened > out[j].Opened || out[i].Opened == out[j].Opened && out[i].Name < out[j].Name
	})
	return out
}

// origin says where a session started: one of Origins.
func origin(s store.Session) string {
	switch {
	case s.ParentID != "":
		return "agent"
	case s.AutomationID != "":
		return "automation"
	case s.Source == "slack" || s.Source == "cli":
		return s.Source
	}
	return "web"
}

// spendKind indexes Day.SpendBy.
func spendKind(s store.Session) int {
	switch {
	case s.ParentID != "":
		return 2
	case s.AutomationID != "":
		return 1
	}
	return 0
}

// better is the more telling of two pull request states.
func better(a, b string) string {
	rank := map[string]int{"": 0, "closed": 1, "open": 2, "merged": 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// bucket indexes PullRequests.ToMerge.
func bucket(secs float64) int {
	for i, limit := range []float64{3600, 4 * 3600, 24 * 3600, 3 * 86400, 7 * 86400} {
		if secs < limit {
			return i
		}
	}
	return 5
}

// personKey says who a session counts for: its account, else its author.
func personKey(s store.Session) string {
	if s.AccountID != "" {
		return "account:" + s.AccountID
	}
	return "author:" + personName(s)
}

func personName(s store.Session) string {
	return firstOf(s.Author.Name, s.Author.Email, "Unknown")
}

func primary(s store.Session) string {
	if r, ok := s.Primary(); ok {
		return r.Repo
	}
	return "scratch workspace"
}

func project(s store.Session, envNames map[string]string) string {
	if s.EnvironmentID == "" {
		return "no project"
	}
	return firstOf(envNames[s.EnvironmentID], "a deleted project")
}

func teamName(s store.Session, teamNames map[string]string) string {
	if s.TeamID == "" {
		return "no team"
	}
	return firstOf(teamNames[s.TeamID], "a deleted team")
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

func rate(n, of int) *float64 {
	if of == 0 {
		return nil
	}
	v := float64(n) / float64(of)
	return &v
}

func rate64(n, of int64) *float64 {
	v := float64(n) / float64(of)
	return &v
}

// quantile is the q-th quantile of v by the nearest rank, except that an
// even count's median averages the middle two; 0 for none. It sorts v.
func quantile(v []float64, q float64) float64 {
	if len(v) == 0 {
		return 0
	}
	sort.Float64s(v)
	n := len(v)
	if q == .5 && n%2 == 0 {
		return (v[n/2-1] + v[n/2]) / 2
	}
	return v[min(n-1, max(0, int(math.Ceil(q*float64(n)))-1))]
}

func firstOf(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
