// Package automation turns schedules and webhooks into session requests.
//
// An automation is a saved request template. Firing it — on its cron
// schedule, from its webhook, or by hand — submits one prompt through the
// same path as every other source; that prompt is the run. Three failed runs
// in a row pause the automation.
package automation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

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
// targets an environment, repositories, or neither.
type Spec struct {
	Name          string          `json:"name"`
	EnvironmentID string          `json:"environment_id"`
	Repos         []store.RepoRef `json:"repos"`
	Prompt        string          `json:"prompt"`
	Model         string          `json:"model"`
	Effort        string          `json:"effort"`
	Schedule      string          `json:"schedule"` // 5-field cron, optionally prefixed "CRON_TZ=Area/City "
	Reuse         bool            `json:"reuse"`
	Enabled       bool            `json:"enabled"`
}

// Create stores a new automation and returns it with its webhook token,
// which is shown only this once.
func (s *Service) Create(ctx context.Context, spec Spec) (store.Automation, string, error) {
	token := randomHex(24)
	a := store.Automation{ID: store.NewID(), HookHash: hash(token)}
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
		if _, err := s.db.Environment(ctx, spec.EnvironmentID); err != nil {
			return fmt.Errorf("%w: environment %q not found", session.ErrInvalid, spec.EnvironmentID)
		}
	}
	a.Name, a.Repos, a.EnvironmentID, a.Prompt = spec.Name, repos, spec.EnvironmentID, spec.Prompt
	a.Model, a.Effort = spec.Model, spec.Effort
	a.Schedule, a.Reuse, a.Enabled = strings.TrimSpace(spec.Schedule), spec.Reuse, spec.Enabled
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
	eff, err := s.m.Effective(ctx, a.EnvironmentID)
	if err != nil {
		return store.Session{}, store.Prompt{}, err
	}
	req := session.Request{
		Prompt: prompt, EnvironmentID: a.EnvironmentID, Repos: a.Repos, Model: a.Model, Effort: a.Effort,
		Title:  a.Name + " · " + time.Now().Format("Jan 2 15:04"),
		Author: eff.Author, Source: source, AutomationID: a.ID,
	}
	if a.Reuse {
		req.Key = "automation:" + a.ID
	}
	req.DedupKey = dedupKey
	return s.m.Submit(ctx, req)
}

// CheckHook authenticates a webhook delivery for automation id.
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
