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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brdsmth/beethoven/internal/agent"
	"github.com/brdsmth/beethoven/internal/event"
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
	Token(ctx context.Context, repo string) (string, error) // credential to clone and push repo; "" if none
	DefaultBranch(ctx context.Context, repo string) (string, error)
	OpenPR(ctx context.Context, pr PullRequest) (url string, err error)
	PRStatus(ctx context.Context, repo, head string) (*PRStatus, error)                        // the latest pull request from head; nil if none
	Comment(ctx context.Context, repo string, number int, body string) (url string, err error) // on an issue or pull request
	AddLabels(ctx context.Context, repo string, number int, labels []string) error
	Author(ctx context.Context) store.Author // the identity of the person the host acts for; zero if unknown
}

type PullRequest struct {
	Repo, Head, Base, Title, Body string
	Draft                         bool
}

// PRStatus is what the git host says about a pull request now.
type PRStatus struct {
	URL            string  `json:"url"`
	Number         int     `json:"number"`
	State          string  `json:"state"` // open, closed or merged
	Draft          bool    `json:"draft"`
	Base           string  `json:"base"`
	HeadSHA        string  `json:"head_sha"`
	Mergeable      *bool   `json:"mergeable"`       // nil while the host is still computing it
	MergeableState string  `json:"mergeable_state"` // GitHub's clean, dirty, blocked, behind, unstable…
	Checks         []Check `json:"checks"`
}

// Check is one CI check or commit status on a pull request's head.
type Check struct {
	Name       string `json:"name"`
	Status     string `json:"status"`               // queued, in_progress or completed
	Conclusion string `json:"conclusion,omitempty"` // success, failure, neutral… once completed
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
	CacheDir    string            // where installed capabilities are kept for later sandboxes; empty: nowhere
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
	Attachments   []store.Attachment // files for the agent: a name, a declared type and the content
	EnvironmentID string
	Repos         []store.RepoRef // the first is the primary
	Model         string          // "" = the effective default, resolved per prompt
	Effort        string
	Title         string
	Author        store.Author
	AccountID     string // the person it's for, whose GitHub it uses; "" = nobody in particular
	TeamID        string // the owning team; "" = the workspace, or a team project's team
	Visibility    string // "" = the team's default, or workspace without a team
	Source        string // web, cli, api, automation, webhook, agent
	ParentID      string // set when an agent spawns a child session
	Key           string // continuation key: reuse the session previously started with this key
	DedupKey      string // a prompt with the same key is only queued once
	AutomationID  string
	Hop           int // agent-to-agent exchanges that led here; 0 for people
}

// Submit queues r.Prompt on the session named by r.Key, or on a new session.
func (m *Manager) Submit(ctx context.Context, r Request) (store.Session, store.Prompt, error) {
	if strings.TrimSpace(r.Prompt) == "" && len(r.Attachments) == 0 {
		return store.Session{}, store.Prompt{}, fmt.Errorf("%w: empty prompt", ErrInvalid)
	}
	in := PromptInput{Content: r.Prompt, Attachments: r.Attachments, Author: r.Author, AccountID: r.AccountID,
		Source: r.Source, DedupKey: r.DedupKey, AutomationID: r.AutomationID, Hop: r.Hop}
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
		// A child works for the same people, seen by the same people.
		r.TeamID, r.AccountID = parent.TeamID, firstOf(r.AccountID, parent.AccountID)
		r.Visibility = firstOf(r.Visibility, parent.Visibility)
	}
	if r.EnvironmentID != "" {
		env, err := m.db.Environment(ctx, r.EnvironmentID)
		if err != nil {
			return store.Session{}, fmt.Errorf("%w: environment %q not found", ErrInvalid, r.EnvironmentID)
		}
		if len(r.Repos) == 0 {
			r.Repos = env.Repos // a snapshot: later edits to the environment don't move the session
		}
		// A team's project starts only that team's sessions.
		if env.TeamID != "" && r.TeamID != "" && r.TeamID != env.TeamID {
			return store.Session{}, fmt.Errorf("%w: %s belongs to another team", ErrInvalid, env.Name)
		}
		r.TeamID = firstOf(r.TeamID, env.TeamID)
	}
	if r.TeamID != "" {
		team, err := m.db.Team(ctx, r.TeamID)
		if err != nil {
			return store.Session{}, fmt.Errorf("%w: team %q not found", ErrInvalid, r.TeamID)
		}
		if team.ArchivedAt != nil && r.ParentID == "" {
			return store.Session{}, fmt.Errorf("%w: %s is archived", ErrInvalid, team.Name)
		}
		r.Visibility = firstOf(r.Visibility, team.DefaultVisibility)
	}
	r.Visibility = firstOf(r.Visibility, store.VisibilityWorkspace)
	if !store.ValidVisibility(r.Visibility, r.TeamID) {
		return store.Session{}, fmt.Errorf("%w: visibility must be workspace, private or, for a team's session, team", ErrInvalid)
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
		AutomationID: r.AutomationID, Author: r.Author, AccountID: r.AccountID, TeamID: r.TeamID,
		Visibility: r.Visibility, Status: "idle", Sandbox: "none",
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
	Attachments  []store.Attachment
	Model        string // overrides the session's for this prompt
	Effort       string
	Author       store.Author
	AccountID    string // who sent it, if a person here
	Source       string
	DedupKey     string
	AutomationID string
	Hop          int // agent-to-agent exchanges that led here; 0 for people
}

// Enqueue adds a prompt to an existing session's queue.
func (m *Manager) Enqueue(ctx context.Context, sessionID string, in PromptInput) (store.Prompt, error) {
	if strings.TrimSpace(in.Content) == "" && len(in.Attachments) == 0 {
		return store.Prompt{}, fmt.Errorf("%w: empty prompt", ErrInvalid)
	}
	if err := ValidateOverrides(store.Overrides{Model: in.Model, Effort: in.Effort}); err != nil {
		return store.Prompt{}, err
	}
	var err error
	if in.Attachments, err = prepareAttachments(in.Attachments); err != nil {
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
// sandbox. Prompting an archived session unarchives it. Sessions are never
// deleted: an archived one keeps its history, for the insights.
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

// PRStatus asks the git host about the pull request from the session's
// branch in repo, as the person the session works for. Nil if none is open.
func (m *Manager) PRStatus(ctx context.Context, sessionID, repo string) (*PRStatus, error) {
	s, err := m.db.Session(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	i, err := pickRepo(s, repo)
	if err != nil {
		return nil, err
	}
	return m.scm.PRStatus(forPerson(ctx, s), s.Repos[i].Repo, s.Branch)
}

// Comment posts on an issue or pull request in one of the session's
// repositories, as the person the session works for. The comment is
// recorded, so when it comes back it isn't sent to the session again, and
// counts as a hop for the agents it reaches; it carries a hidden marker
// saying so is on its way (see Origin). The session follows the thread from then on: replies come back
// to it.
func (m *Manager) Comment(ctx context.Context, sessionID, repo string, number int, body string) (string, error) {
	s, ref, err := m.threadIn(ctx, sessionID, repo, number)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("%w: the comment is empty", ErrInvalid)
	}
	hop, promptID := 0, ""
	if r, err := m.runner(ctx, sessionID); err == nil {
		hop, promptID = r.currentHop(), r.currentPrompt()
	}
	url, err := m.scm.Comment(forPerson(ctx, s), ref, number, event.Mark(body, s.ID, hop))
	if err != nil {
		return url, err
	}
	if url != "" {
		if err := m.db.AddOutbound(ctx, store.Outbound{Ref: url, SessionID: s.ID, PromptID: promptID, Hop: hop}); err != nil {
			m.log.Error("record comment", "session", s.ID, "err", err)
		}
	}
	m.link(ctx, s.ID, store.LinkFollower, "commented", event.ThreadOf(event.GitHub, ref, fmt.Sprintf("issue:%d", number)))
	return url, nil
}

// MaxHops is how many exchanges between agents may lead to a prompt: an
// agent's words setting another agent to work are one more each time.
const MaxHops = 4

// OriginWait is how long Origin waits for a comment carrying the marker to
// be recorded: GitHub can tell us about a comment before the call posting
// it has returned.
var OriginWait = 1500 * time.Millisecond

// Origin says which of this server's sessions posted the comment an event
// carries, and at what hop, from what the server recorded posting. Only
// the record counts: the marker in the text is a hint that a record is on
// its way, which with wait set is worth waiting a moment for, but anyone
// can type a marker, or copy one when quoting an agent.
func (m *Manager) Origin(ctx context.Context, e event.Event, wait bool) (event.Provenance, bool) {
	if e.CommentURL == "" {
		return event.Provenance{}, false
	}
	_, hinted := event.Marked(e.Comment)
	deadline := time.Now().Add(OriginWait)
	for {
		if o, err := m.db.OutboundByRef(ctx, e.CommentURL); err == nil {
			return event.Provenance{SessionID: o.SessionID, Hop: o.Hop}, true
		}
		if !wait || !hinted || time.Now().After(deadline) {
			return event.Provenance{}, false
		}
		select {
		case <-ctx.Done():
			return event.Provenance{}, false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Follow makes a session hear about what happens on an issue or pull request
// in one of its repositories.
func (m *Manager) Follow(ctx context.Context, sessionID, repo string, number int) (string, error) {
	_, ref, err := m.threadIn(ctx, sessionID, repo, number)
	if err != nil {
		return "", err
	}
	subject := event.ThreadOf(event.GitHub, ref, fmt.Sprintf("issue:%d", number))
	return subject, m.db.AddLink(ctx, store.Link{Subject: subject, SessionID: sessionID, Role: store.LinkFollower, Reason: "asked"})
}

// Unfollow stops a session hearing about an issue or pull request.
func (m *Manager) Unfollow(ctx context.Context, sessionID, repo string, number int) error {
	_, ref, err := m.threadIn(ctx, sessionID, repo, number)
	if err != nil {
		return err
	}
	return m.db.RemoveLink(ctx, event.ThreadOf(event.GitHub, ref, fmt.Sprintf("issue:%d", number)), sessionID)
}

// Note puts something on a session's timeline without running its agent:
// what happened on a thread it follows, and why it wasn't handed over.
func (m *Manager) Note(ctx context.Context, sessionID string, data map[string]string) {
	if r, err := m.runner(ctx, sessionID); err == nil {
		r.emit(r.currentPrompt(), EvThread, data)
	}
}

// link makes a session take part in a thread; failing only costs the
// session hearing about it, so it's logged.
func (m *Manager) link(ctx context.Context, sessionID, role, reason, subject string) {
	if err := m.db.AddLink(context.WithoutCancel(ctx), store.Link{Subject: subject, SessionID: sessionID, Role: role, Reason: reason}); err != nil {
		m.log.Error("link session", "session", sessionID, "subject", subject, "err", err)
	}
}

// Link makes a session take part in a thread, as a follower: for sources
// that start sessions about one, like automations fired by an event.
func (m *Manager) Link(ctx context.Context, sessionID, reason, subject string) {
	m.link(ctx, sessionID, store.LinkFollower, reason, subject)
}

var pullNumber = regexp.MustCompile(`/pull/(\d+)`)

// prNumber reads a pull request's number from its URL; 0 if it has none.
func prNumber(url string) int {
	m := pullNumber.FindStringSubmatch(url)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// AddLabels labels an issue or pull request in one of the session's
// repositories, as the person the session works for.
func (m *Manager) AddLabels(ctx context.Context, sessionID, repo string, number int, labels []string) error {
	s, ref, err := m.threadIn(ctx, sessionID, repo, number)
	if err != nil {
		return err
	}
	if len(labels) == 0 {
		return fmt.Errorf("%w: no labels given", ErrInvalid)
	}
	return m.scm.AddLabels(forPerson(ctx, s), ref, number, labels)
}

func (m *Manager) threadIn(ctx context.Context, sessionID, repo string, number int) (store.Session, string, error) {
	s, err := m.db.Session(ctx, sessionID)
	if err != nil {
		return s, "", err
	}
	if number <= 0 {
		return s, "", fmt.Errorf("%w: say which issue or pull request number", ErrInvalid)
	}
	i, err := pickRepo(s, repo)
	if err != nil {
		return s, "", err
	}
	return s, s.Repos[i].Repo, nil
}

// SetVisibility changes who may read a session.
func (m *Manager) SetVisibility(ctx context.Context, sessionID, visibility string) (store.Session, error) {
	r, err := m.runner(ctx, sessionID)
	if err != nil {
		return store.Session{}, err
	}
	if s := r.snapshot(); !store.ValidVisibility(visibility, s.TeamID) {
		return store.Session{}, fmt.Errorf("%w: visibility must be workspace, private or, for a team's session, team", ErrInvalid)
	}
	r.update(func(s *store.Session) { s.Visibility = visibility })
	return r.snapshot(), nil
}

// Share sets the people a session is shared with, who may read it.
func (m *Manager) Share(ctx context.Context, sessionID string, accountIDs []string) (store.Session, error) {
	r, err := m.runner(ctx, sessionID)
	if err != nil {
		return store.Session{}, err
	}
	shared, err := m.db.ShareSession(ctx, sessionID, accountIDs)
	if err != nil {
		return store.Session{}, err
	}
	r.update(func(s *store.Session) { s.SharedWith = shared })
	return r.snapshot(), nil
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
	if line == "" && len(r.Attachments) > 0 {
		line = fileName(r.Attachments[0].Name)
	}
	return clip(line, 80)
}
