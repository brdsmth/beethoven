// Package automation turns schedules, webhooks and git host events into
// session requests.
//
// An automation is a saved request template. Firing it — on its cron
// schedule, from its webhook, on a matching event, or by hand — submits one
// prompt through the same path as every other source; that prompt is the run. Three failed runs
// in a row pause the automation.
package automation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/brdsmth/beethoven/internal/event"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

const MaxFailures = 3

var (
	ErrBusy     = errors.New("the automation's previous run is still in progress")
	ErrDisabled = errors.New("the automation is paused")
)

type Service struct {
	db  *store.Store
	m   *session.Manager
	log *slog.Logger
}

func New(m *session.Manager, log *slog.Logger) *Service {
	s := &Service{db: m.Store(), m: m, log: log}
	m.AddNotifier(s)
	return s
}

// Spec is the user-editable part of an automation. Like a session, it
// targets an environment, repositories, or neither, and may belong to a
// team (fixed once made).
type Spec struct {
	Name          string          `json:"name"`
	TeamID        string          `json:"team_id"`
	EnvironmentID string          `json:"environment_id"`
	Repos         []store.RepoRef `json:"repos"`
	Prompt        string          `json:"prompt"`
	Model         string          `json:"model"`
	Effort        string          `json:"effort"`
	Schedule      string          `json:"schedule"` // 5-field cron, optionally prefixed "CRON_TZ=Area/City "
	Reuse         bool            `json:"reuse"`
	Enabled       bool            `json:"enabled"`
	// Trigger fires it on events from the git host, in the repositories it
	// targets. With Reuse, every event about the same issue or pull request
	// goes to one session.
	Trigger *event.Trigger `json:"trigger,omitempty"`
}

// Create stores a new automation, which runs as accountID ("" for the
// server's owner), and returns it with its webhook token, which is shown
// only this once.
func (s *Service) Create(ctx context.Context, spec Spec, accountID string) (store.Automation, string, error) {
	token := randomHex(24)
	a := store.Automation{ID: store.NewID(), HookHash: hash(token), TeamID: spec.TeamID, AccountID: accountID}
	if err := s.apply(ctx, &a, spec, time.Now()); err != nil {
		return a, "", err
	}
	return a, token, s.db.CreateAutomation(ctx, &a)
}

func (s *Service) Update(ctx context.Context, id string, spec Spec) (store.Automation, error) {
	a, err := s.db.Automation(ctx, id)
	if err != nil {
		return a, err
	}
	if spec.Enabled && !a.Enabled {
		a.Failures = 0 // re-enabling clears the strike count
	}
	if err := s.apply(ctx, &a, spec, time.Now()); err != nil {
		return a, err
	}
	return a, s.db.SaveAutomation(ctx, &a)
}

func (s *Service) apply(ctx context.Context, a *store.Automation, spec Spec, now time.Time) error {
	if strings.TrimSpace(spec.Name) == "" || strings.TrimSpace(spec.Prompt) == "" {
		return fmt.Errorf("%w: name and prompt are required", session.ErrInvalid)
	}
	if err := session.ValidateOverrides(store.Overrides{Model: spec.Model, Effort: spec.Effort}); err != nil {
		return err
	}
	repos, err := session.ValidateRepos(spec.Repos)
	if err != nil {
		return err
	}
	if spec.EnvironmentID != "" {
		if len(repos) > 0 {
			return fmt.Errorf("%w: choose an environment or repositories, not both", session.ErrInvalid)
		}
		e, err := s.db.Environment(ctx, spec.EnvironmentID)
		if err != nil {
			return fmt.Errorf("%w: environment %q not found", session.ErrInvalid, spec.EnvironmentID)
		}
		if e.TeamID != "" && e.TeamID != a.TeamID {
			return fmt.Errorf("%w: %s belongs to another team", session.ErrInvalid, e.Name)
		}
	}
	a.Name, a.Repos, a.EnvironmentID, a.Prompt = spec.Name, repos, spec.EnvironmentID, spec.Prompt
	a.Model, a.Effort = spec.Model, spec.Effort
	a.Schedule, a.Reuse, a.Enabled = strings.TrimSpace(spec.Schedule), spec.Reuse, spec.Enabled
	a.TriggerEvent, a.TriggerConditions = "", nil
	if t := spec.Trigger; t != nil && t.Event != "" {
		if err := t.Validate(); err != nil {
			return fmt.Errorf("%w: trigger: %v", session.ErrInvalid, err)
		}
		if len(repos) == 0 && spec.EnvironmentID == "" {
			return fmt.Errorf("%w: an event trigger needs repositories or a project to watch", session.ErrInvalid)
		}
		if t.Conditions == nil {
			t.Conditions = []event.Condition{}
		}
		a.TriggerEvent = t.Event
		a.TriggerConditions, _ = json.Marshal(t.Conditions)
	}
	a.NextRunAt = nil
	if a.Schedule != "" {
		sched, err := cron.ParseStandard(a.Schedule)
		if err != nil {
			return fmt.Errorf("%w: schedule: %v", session.ErrInvalid, err)
		}
		next := sched.Next(now)
		a.NextRunAt = &next
	}
	return nil
}

// Fire submits one run. payload, if any, is appended to the prompt;
// dedupKey makes repeated deliveries of the same event fire once.
func (s *Service) Fire(ctx context.Context, a store.Automation, source, payload, dedupKey string) (store.Session, store.Prompt, error) {
	if dedupKey != "" {
		dedupKey = "automation:" + a.ID + ":" + dedupKey
		if p, err := s.db.PromptByDedupKey(ctx, dedupKey); err == nil {
			sess, err := s.db.Session(ctx, p.SessionID) // a redelivery: report the original run
			return sess, p, err
		}
	}
	if !a.Enabled {
		return store.Session{}, store.Prompt{}, ErrDisabled
	}
	busy, err := s.db.AutomationBusy(ctx, a.ID)
	if err != nil {
		return store.Session{}, store.Prompt{}, err
	}
	if busy {
		return store.Session{}, store.Prompt{}, ErrBusy
	}
	prompt := a.Prompt
	if payload != "" {
		prompt += "\n\nThe " + source + " that triggered this run delivered:\n<payload>\n" + payload + "\n</payload>"
	}
	if err := s.mayRun(ctx, a); err != nil {
		return store.Session{}, store.Prompt{}, err
	}
	req := s.request(ctx, a, source, prompt, a.Name+" · "+time.Now().Format("Jan 2 15:04"))
	if a.Reuse {
		req.Key = "automation:" + a.ID
	}
	req.DedupKey = dedupKey
	return s.m.Submit(ctx, req)
}

func (s *Service) request(ctx context.Context, a store.Automation, source, prompt, title string) session.Request {
	return session.Request{
		Prompt: prompt, EnvironmentID: a.EnvironmentID, Repos: a.Repos, Model: a.Model, Effort: a.Effort, Title: title,
		Author: s.m.AuthorFor(ctx, a.AccountID), AccountID: a.AccountID, TeamID: a.TeamID, Source: source, AutomationID: a.ID,
	}
}

// Dispatch fires every enabled automation whose trigger matches e and that
// watches e's repository, and returns the runs it started. Unlike other
// sources, events don't wait for an automation's previous run: each is
// about its own issue or pull request, and with Reuse, events about the
// same one queue on its session.
//
// What this server's own agents post can fire automations too, so one
// agent's review sets another to work, within the same bounds as the relay:
// an automation never fires on its own runs' words, and a chain of agents
// stops after session.MaxHops. Only what the server recorded posting counts
// as its agents': anyone can type the marker.
func (s *Service) Dispatch(ctx context.Context, e event.Event) ([]store.Prompt, error) {
	o := s.origin(ctx, e, true)
	cands, err := s.db.TriggeredAutomations(ctx, e.Kind)
	if err != nil {
		return nil, err
	}
	var started []store.Prompt
	var errs []error
	for _, a := range cands {
		if !s.fires(ctx, a, e, o) {
			continue
		}
		_, p, err := s.fireEvent(ctx, a, e, o.hop)
		if err != nil {
			errs = append(errs, fmt.Errorf("automation %s: %w", a.ID, err))
			continue
		}
		started = append(started, p)
	}
	return started, errors.Join(errs...)
}

// origin is who among this server's sessions said what an event carries.
type origin struct {
	ours bool
	hop  int    // of the prompt an event would start: one more than the words' own
	from string // the automation whose run said it, if one did
}

func (s *Service) origin(ctx context.Context, e event.Event, wait bool) origin {
	p, ok := s.m.Origin(ctx, e, wait)
	if !ok {
		return origin{}
	}
	o := origin{ours: true, hop: p.Hop + 1}
	if sess, err := s.db.Session(ctx, p.SessionID); err == nil {
		o.from = sess.AutomationID
	}
	return o
}

// fires reports whether a fires on e: it's on, its trigger matches, it
// watches e's repository, e isn't another app's comment nor its own run's,
// and the chain of agents behind e isn't too long. (Whether its person may
// still run it is checked when it runs, which says why not.)
func (s *Service) fires(ctx context.Context, a store.Automation, e event.Event, o origin) bool {
	switch {
	case !a.Enabled || a.TriggerEvent != e.Kind || !Trigger(a).Matches(e) || !s.watches(ctx, a, e.Repo):
		return false
	case e.Bot && e.Comment != "" && !o.ours:
		return false // other apps' comments and reviews would set off loops
	case o.ours && (o.from == a.ID || o.hop > session.MaxHops):
		return false
	}
	return true
}

// Fires reports whether the automation fires on e itself, so its runs
// needn't be told about e another way.
func (s *Service) Fires(ctx context.Context, automationID string, e event.Event) bool {
	a, err := s.db.Automation(ctx, automationID)
	return err == nil && s.fires(ctx, a, e, s.origin(ctx, e, false)) && s.mayRun(ctx, a) == nil
}

// Trigger is an automation's stored trigger.
func Trigger(a store.Automation) event.Trigger {
	t := event.Trigger{Event: a.TriggerEvent, Conditions: []event.Condition{}}
	json.Unmarshal(a.TriggerConditions, &t.Conditions)
	return t
}

// watches reports whether repo is one the automation targets, directly or
// through its project.
func (s *Service) watches(ctx context.Context, a store.Automation, repo string) bool {
	repos := a.Repos
	if a.EnvironmentID != "" {
		env, err := s.db.Environment(ctx, a.EnvironmentID)
		if err != nil {
			return false
		}
		repos = env.Repos
	}
	return slices.ContainsFunc(repos, func(r store.RepoRef) bool { return strings.EqualFold(r.Repo, repo) })
}

func (s *Service) fireEvent(ctx context.Context, a store.Automation, e event.Event, hop int) (store.Session, store.Prompt, error) {
	dedupKey := "automation:" + a.ID + ":" + e.Host + ":" + e.Occurrence
	if p, err := s.db.PromptByDedupKey(ctx, dedupKey); err == nil {
		sess, err := s.db.Session(ctx, p.SessionID)
		return sess, p, err
	}
	if err := s.mayRun(ctx, a); err != nil {
		return store.Session{}, store.Prompt{}, err
	}
	title := a.Name + " · " + e.Repo
	if e.Number > 0 {
		title = fmt.Sprintf("%s · #%d %s", a.Name, e.Number, e.Title)
	}
	req := s.request(ctx, a, "event", a.Prompt+"\n\n"+e.Describe(), title)
	if a.EnvironmentID == "" {
		// Work in the repository the event happened in, on the pull
		// request's branch when it has one there.
		ref := store.RepoRef{Repo: e.Repo}
		if i := slices.IndexFunc(a.Repos, func(r store.RepoRef) bool { return strings.EqualFold(r.Repo, e.Repo) }); i >= 0 {
			ref = a.Repos[i]
		}
		if strings.HasPrefix(e.Subject, "pr:") && e.Branch != "" && !e.Fork && e.Kind != event.PRMerged && e.Kind != event.PRClosed {
			ref.BaseBranch = e.Branch
		}
		req.Repos = []store.RepoRef{ref}
	}
	if a.Reuse {
		req.Key = "automation:" + a.ID + ":" + e.Host + ":" + e.Repo + ":" + e.Subject
	}
	req.DedupKey, req.Hop = dedupKey, hop
	sess, p, err := s.m.Submit(ctx, req)
	if err == nil && e.Number > 0 {
		// The run takes part in the thread it was started for: what's said
		// there afterwards comes back to it.
		s.m.Link(ctx, sess.ID, "triggered", e.Thread())
	}
	return sess, p, err
}

// mayRun checks the person an automation runs as may still run it: their
// account is active and, for a team's automation, they're still in the
// team, which isn't archived.
func (s *Service) mayRun(ctx context.Context, a store.Automation) error {
	if a.AccountID == "" {
		return nil
	}
	acct, err := s.db.Account(ctx, a.AccountID)
	if err != nil || acct.Disabled {
		return fmt.Errorf("%w: the person it runs as is no longer here; recreate it", ErrDisabled)
	}
	if a.TeamID == "" {
		return nil
	}
	team, err := s.db.Team(ctx, a.TeamID)
	if err != nil || team.ArchivedAt != nil {
		return fmt.Errorf("%w: its team is archived", ErrDisabled)
	}
	if m, _ := s.db.Memberships(ctx, a.AccountID); m[a.TeamID] == "" && !acct.Admin() {
		return fmt.Errorf("%w: %s is no longer in %s", ErrDisabled, firstOf(acct.Name, acct.Email), team.Name)
	}
	return nil
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// CheckHook authenticates a webhook delivery for automation id.
// NewHookToken gives an automation a new webhook token, which replaces the
// one before; only its hash is kept.
func (s *Service) NewHookToken(ctx context.Context, id string) (string, error) {
	token := randomHex(24)
	return token, s.db.SetAutomationHookHash(ctx, id, hash(token))
}

func (s *Service) CheckHook(ctx context.Context, id, token string) (store.Automation, error) {
	a, err := s.db.Automation(ctx, id)
	if err != nil {
		return a, err
	}
	if subtle.ConstantTimeCompare([]byte(hash(token)), []byte(a.HookHash)) != 1 {
		return a, session.ErrUnauthorized
	}
	return a, nil
}

// Run fires due schedules until ctx ends.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		s.tick(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Service) tick(ctx context.Context, now time.Time) {
	due, err := s.db.DueAutomations(ctx, now)
	if err != nil {
		s.log.Error("due automations", "err", err)
		return
	}
	for _, a := range due {
		sched, err := cron.ParseStandard(a.Schedule)
		if err != nil {
			continue
		}
		// Advance first: each due time fires at most once, even if two
		// servers tick together, and missed times collapse into one run.
		if ok, err := s.db.AdvanceSchedule(ctx, a.ID, *a.NextRunAt, sched.Next(now)); err != nil || !ok {
			continue
		}
		if _, _, err := s.Fire(ctx, a, "schedule", "", ""); err != nil {
			s.log.Info("scheduled run skipped", "automation", a.ID, "err", err)
		}
	}
}

// PromptFinished keeps the consecutive-failure count and pauses an
// automation after MaxFailures.
func (s *Service) PromptFinished(ctx context.Context, _ store.Session, p store.Prompt) {
	if p.AutomationID == "" {
		return
	}
	a, err := s.db.Automation(ctx, p.AutomationID)
	if err != nil {
		return
	}
	switch p.Status {
	case store.Done:
		a.Failures = 0
	case store.Failed:
		a.Failures++
		if a.Failures >= MaxFailures {
			a.Enabled = false
			s.log.Warn("automation paused after repeated failures", "automation", a.ID)
		}
	default:
		return
	}
	if err := s.db.SaveAutomation(ctx, &a); err != nil {
		s.log.Error("save automation", "automation", a.ID, "err", err)
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
