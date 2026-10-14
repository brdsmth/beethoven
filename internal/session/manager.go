// Package session runs background coding sessions.
//
// A session is a prompt queue, an event log and a sandbox, owned by one
// runner. Every way of starting work — the web UI, the CLI, automations,
// webhooks, or an agent spawning a child — goes through Manager.Submit.
package session

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/brdsmth/beethoven/internal/agent"
	"github.com/brdsmth/beethoven/internal/sandbox"
	"github.com/brdsmth/beethoven/internal/store"
)

//go:embed boot.sh
var bootScript string


var (
	ErrNotFound     = store.ErrNotFound
	ErrInvalid      = store.ErrInvalid
	ErrDepth        = errors.New("child session depth limit reached")
	ErrUnauthorized = errors.New("invalid session token")
)

// SCM is the git host: where repositories are cloned from and pull requests
// are opened.
type SCM interface {
	CloneURL(repo string) string
	Token(ctx context.Context) (string, error) // credential for clone and push; "" if none
	DefaultBranch(ctx context.Context, repo string) (string, error)
	OpenPR(ctx context.Context, pr PullRequest) (url string, err error)
}

type PullRequest struct {
	Repo, Head, Base, Title, Body string
	Draft                         bool
}

// Notifier hears about every finished prompt. Integrations (automations
// today; chat and issue trackers later) report results through it.
type Notifier interface {
	PromptFinished(ctx context.Context, s store.Session, p store.Prompt)
}

// Config holds the server's defaults; the global settings and environments
// override most of them (see Effective).
type Config struct {
	Image       string
	IdleTimeout time.Duration     // stop a sandbox after this long without work
	TurnTimeout time.Duration     // upper bound on one prompt
	Author      store.Author      // default commit author for prompts from people
	ToolsURL    string            // MCP endpoint as reached from inside a sandbox
	TokenKey    []byte            // signs session tool tokens
	MaxDepth    int               // child session nesting limit
	Committer   store.Author      // git committer; prompt authors are the commit authors
	Env         map[string]string // base sandbox environment; secrets override it
}

type Manager struct {
	cfg   Config
	db    *store.Store
	box   sandbox.Provider
	agent agent.Harness
	scm   SCM
	hub   *Hub
	log   *slog.Logger

	ctx    context.Context // cancelled on shutdown; parent of every turn
	cancel context.CancelFunc
	turns  sync.WaitGroup

	mu        sync.Mutex
	runners   map[string]*runner
	notifiers []Notifier
}

func New(cfg Config, db *store.Store, box sandbox.Provider, h agent.Harness, scm SCM, log *slog.Logger) *Manager {
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 10 * time.Minute
	}
	if cfg.TurnTimeout == 0 {
		cfg.TurnTimeout = 2 * time.Hour
	}
	if cfg.MaxDepth == 0 {
		cfg.MaxDepth = 2
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{cfg: cfg, db: db, box: box, agent: h, scm: scm, hub: NewHub(), log: log,
		ctx: ctx, cancel: cancel, runners: map[string]*runner{}}
}

func (m *Manager) Hub() *Hub              { return m.hub }
func (m *Manager) AddNotifier(n Notifier) { m.notifiers = append(m.notifiers, n) }
func (m *Manager) Store() *store.Store    { return m.db }
func (m *Manager) Config() Config         { return m.cfg }

// Start recovers from a previous process: prompts it left running are
// failed, sandboxes it left up are stopped, and queued work resumes.
func (m *Manager) Start(ctx context.Context) error {
	interrupted, err := m.db.RecoverRunning(ctx, "interrupted: the server restarted")
	if err != nil {
		return err
	}
	var kick []*runner // sessions to run or settle once sandboxes are reset
	for _, p := range interrupted {
		if r, err := m.runner(ctx, p.SessionID); err == nil {
			r.emit(p.ID, EvEnd, endData(p))
			kick = append(kick, r)
		}
	}
	up, err := m.db.Sessions(ctx, store.SessionFilter{Sandbox: []string{"starting", "ready"}, Limit: 10000})
	if err != nil {
		return err
	}
	for _, s := range up {
		r, err := m.runner(ctx, s.ID)
		if err != nil {
			continue
		}
		// Whatever state these sandboxes are in, their workspace must boot
		// again before use; stop them unless new work claims them first.
		r.setSandbox("stopped", "", "the server restarted")
		go r.reclaim()
	}
	queued, err := m.db.QueuedSessions(ctx)
	if err != nil {
		return err
	}
	for _, id := range queued {
		if r, err := m.runner(ctx, id); err == nil {
			kick = append(kick, r)
		}
	}
	for _, r := range kick {
		r.kick()
	}
	return nil
}

// Shutdown cancels running turns and waits briefly for them to record their outcome.
func (m *Manager) Shutdown(timeout time.Duration) {
	m.cancel()
	done := make(chan struct{})
	go func() { m.turns.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// Request is a unit of work from any source. It targets an environment,
// a list of repositories, or (with neither) a scratch workspace.
type Request struct {
	Prompt        string
	EnvironmentID string
	Repos         []store.RepoRef // the first is the primary
	Model         string          // "" = the effective default, resolved per prompt
	Effort        string
	Title         string
	Author       store.Author
	Source       string // web, cli, api, automation, webhook, agent
	ParentID     string // set when an agent spawns a child session
	Key          string // continuation key: reuse the session previously started with this key
	DedupKey     string // a prompt with the same key is only queued once
	AutomationID string
}

// Submit queues r.Prompt on the session named by r.Key, or on a new session.
func (m *Manager) Submit(ctx context.Context, r Request) (store.Session, store.Prompt, error) {
	if strings.TrimSpace(r.Prompt) == "" {
		return store.Session{}, store.Prompt{}, fmt.Errorf("%w: empty prompt", ErrInvalid)
	}
	in := PromptInput{Content: r.Prompt, Author: r.Author, Source: r.Source, DedupKey: r.DedupKey, AutomationID: r.AutomationID}
	if r.Key != "" {
		// A continued session keeps its own settings; carry the request's
		// choices on the prompt instead.
		in.Model, in.Effort = r.Model, r.Effort
	}
	for range 2 {
		if r.Key != "" {
			if s, err := m.db.SessionByKey(ctx, r.Key); err == nil {
				p, err := m.Enqueue(ctx, s.ID, in)
				return s, p, err
			}
		}
		s, err := m.create(ctx, r)
		if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: sessions.key") {
			continue // lost a race with another request for the same key
		}
		if err != nil {
			return s, store.Prompt{}, err
		}
		p, err := m.Enqueue(ctx, s.ID, in)
		return s, p, err
	}
	return store.Session{}, store.Prompt{}, errors.New("session key contention")
}

func (m *Manager) create(ctx context.Context, r Request) (store.Session, error) {
	if r.EnvironmentID != "" && len(r.Repos) > 0 {
		return store.Session{}, fmt.Errorf("%w: choose an environment or repositories, not both", ErrInvalid)
	}
	if err := ValidateOverrides(store.Overrides{Model: r.Model, Effort: r.Effort}); err != nil {
		return store.Session{}, err
	}
	depth := 0
	if r.ParentID != "" {
		parent, err := m.db.Session(ctx, r.ParentID)
		if err != nil {
			return store.Session{}, err
		}
		if depth = parent.Depth + 1; depth > m.cfg.MaxDepth {
			return store.Session{}, ErrDepth
		}
		if r.EnvironmentID == "" && len(r.Repos) == 0 { // work where the parent works
			r.EnvironmentID = parent.EnvironmentID
			for _, ref := range parent.Repos {
				r.Repos = append(r.Repos, store.RepoRef{Repo: ref.Repo, BaseBranch: ref.BaseBranch})
			}
		}
		r.Model, r.Effort = firstOf(r.Model, parent.Model), firstOf(r.Effort, parent.Effort)
	}
	if r.EnvironmentID != "" && len(r.Repos) == 0 {
		env, err := m.db.Environment(ctx, r.EnvironmentID)
		if err != nil {
			return store.Session{}, fmt.Errorf("%w: environment %q not found", ErrInvalid, r.EnvironmentID)
		}
		r.Repos = env.Repos // a snapshot: later edits to the environment don't move the session
	}
	repos, err := ValidateRepos(r.Repos)
	if err != nil {
		return store.Session{}, err
	}
	eff, err := m.Effective(ctx, r.EnvironmentID)
	if err != nil {
		return store.Session{}, err
	}
	id := store.NewID()
	s := store.Session{
		ID: id, Title: titleOf(r), Repos: repos, EnvironmentID: r.EnvironmentID, Branch: eff.BranchPrefix + id,
		Model: r.Model, Effort: r.Effort, ParentID: r.ParentID, Depth: depth, Source: r.Source, Key: r.Key,
		AutomationID: r.AutomationID, Author: r.Author, Status: "idle", Sandbox: "none",
	}
	if err := m.db.CreateSession(ctx, &s); err != nil {
		return s, err
	}
	m.publishSession(s)
	if s.ParentID != "" {
		if parent, err := m.runner(ctx, s.ParentID); err == nil {
			parent.emit(parent.currentPrompt(), EvChild, map[string]string{"id": s.ID, "title": s.Title, "status": "running"})
		}
	}
	return s, nil
}

type PromptInput struct {
	Content      string
	Model        string // overrides the session's for this prompt
	Effort       string
	Author       store.Author
	Source       string
	DedupKey     string
	AutomationID string
}

// Enqueue adds a prompt to an existing session's queue.
func (m *Manager) Enqueue(ctx context.Context, sessionID string, in PromptInput) (store.Prompt, error) {
	if strings.TrimSpace(in.Content) == "" {
		return store.Prompt{}, fmt.Errorf("%w: empty prompt", ErrInvalid)
	}
	if err := ValidateOverrides(store.Overrides{Model: in.Model, Effort: in.Effort}); err != nil {
		return store.Prompt{}, err
	}
	r, err := m.runner(ctx, sessionID)
	if err != nil {
		return store.Prompt{}, err
	}
	return r.enqueue(ctx, in)
}

// Stop ends the running prompt and cancels everything queued behind it.
func (m *Manager) Stop(ctx context.Context, sessionID string) error {
	r, err := m.runner(ctx, sessionID)
	if err != nil {
		return err
	}
	r.stop("")
	return nil
}

// CancelPrompt cancels one prompt, stopping it if it is the one running.
func (m *Manager) CancelPrompt(ctx context.Context, sessionID, promptID string) error {
	r, err := m.runner(ctx, sessionID)
	if err != nil {
		return err
	}
	r.stop(promptID)
	return nil
}

// Archive hides a session from the default list, stopping its work and its
// sandbox. Prompting an archived session unarchives it.
func (m *Manager) Archive(ctx context.Context, sessionID string, archived bool) error {
	r, err := m.runner(ctx, sessionID)
	if err != nil {
		return err
	}
	if archived {
		r.stop("")
		go r.stopSandbox("archived")
	}
	r.update(func(s *store.Session) { s.Archived = archived })
	return nil
}

// Delete removes a session, its sandbox and its history.
func (m *Manager) Delete(ctx context.Context, sessionID string) error {
	r, err := m.runner(ctx, sessionID)
	if err != nil {
		return err
	}
	if err := r.destroy(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.runners, sessionID)
	m.mu.Unlock()
	m.hub.Publish(AllSessions, store.Event{Session: sessionID, Type: EvDeleted, At: time.Now(),
		Data: mustJSON(map[string]string{"id": sessionID})})
	return nil
}

// OpenPR pushes the session's branch in repo (which may be empty when the
// session has one repository) and opens, or finds, its pull request. A nil
// draft uses the settings' default.
func (m *Manager) OpenPR(ctx context.Context, sessionID, repo, title, body string, draft *bool) (string, error) {
	r, err := m.runner(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return r.openPR(ctx, repo, title, body, draft)
}

func (m *Manager) Session(ctx context.Context, id string) (store.Session, error) {
	return m.db.Session(ctx, id)
}

// Token is the credential a session's agent uses for the tool server.
func (m *Manager) Token(sessionID string) string {
	mac := hmac.New(sha256.New, m.cfg.TokenKey)
	mac.Write([]byte(sessionID))
	return sessionID + "." + hex.EncodeToString(mac.Sum(nil))
}

// Authenticate resolves a tool token to its session.
func (m *Manager) Authenticate(ctx context.Context, token string) (store.Session, error) {
	id, _, ok := strings.Cut(token, ".")
	if !ok || !hmac.Equal([]byte(token), []byte(m.Token(id))) {
		return store.Session{}, ErrUnauthorized
	}
	return m.db.Session(ctx, id)
}

func (m *Manager) runner(ctx context.Context, id string) (*runner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.runners[id]; ok {
		return r, nil
	}
	s, err := m.db.Session(ctx, id)
	if err != nil {
		return nil, err
	}
	r := &runner{m: m, id: id, s: s}
	if ps, err := m.db.Prompts(ctx, id); err == nil {
		for _, p := range ps {
			if p.Status != store.Queued && p.Status != store.Running {
				r.last = p.Status
			}
		}
	}
	m.runners[id] = r
	return r, nil
}

func (m *Manager) publishSession(s store.Session) {
	e := store.Event{Session: s.ID, Type: EvSession, Data: mustJSON(s), At: time.Now()}
	m.hub.Publish(s.ID, e)
	m.hub.Publish(AllSessions, e)
}

func (m *Manager) finished(s store.Session, p store.Prompt) {
	ctx := context.Background()
	if s.ParentID != "" {
		if parent, err := m.runner(ctx, s.ParentID); err == nil {
			parent.emit("", EvChild, map[string]string{"id": s.ID, "title": s.Title, "status": p.Status, "error": p.Error})
		}
	}
	for _, n := range m.notifiers {
		n.PromptFinished(ctx, s, p)
	}
}

func titleOf(r Request) string {
	if t := strings.TrimSpace(r.Title); t != "" {
		return t
	}
	line, _, _ := strings.Cut(strings.TrimSpace(r.Prompt), "\n")
	return clip(line, 80)
}
