// Package cloud runs Beethoven as a hosted service: many workspaces in one
// process, each with its own database and key, under one domain.
//
//	<slug>.<domain>     a workspace: its UI, API and webhooks
//	app.<domain>        the service: where GitHub and Slack call back, and
//	                    the operator's admin pages
//	*/ws/<slug>/mcp     a workspace's agent tools, as sandboxes reach them
//
// GitHub and Slack call back to one address for every workspace. A browser
// coming back from them is sent on to its workspace, which the round
// trip's state names; Slack's events are passed to the workspace that
// added the Slack workspace they come from.
//
// A workspace's database opens, migrating as needed, when the workspace is
// first used; at boot every active one opens in the background, so
// schedules run and migration failures show early. Before migrating, the
// database is copied aside; a workspace that fails to open is unavailable
// until an operator retries, and the others are unaffected.
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brdsmth/beethoven/internal/sandbox"
	"github.com/brdsmth/beethoven/internal/slack"
	"github.com/brdsmth/beethoven/internal/store"
	"github.com/brdsmth/beethoven/internal/workspace"
)

// Config configures the service.
type Config struct {
	Domain     string // workspaces are <slug>.<Domain>; the service is app.<Domain>
	Scheme     string // https; http for local testing (with Domain "localhost")
	Port       string // in addresses, when it isn't the scheme's own: for local testing
	DataDir    string // holds cloud.db and workspaces/<id>/
	MasterKey  []byte // seals each workspace's key
	AdminToken string // the operator's password for /admin

	// Workspace is what every workspace shares: the apps, the image,
	// timeouts. Each workspace's own parts are filled in for it.
	Workspace workspace.Config
	// Sandboxes returns the sandbox provider for a workspace.
	Sandboxes func(slug string) (sandbox.Provider, error)
	// ToolsBase is this server as sandboxes reach it: a workspace's agent
	// tools are at ToolsBase/ws/<slug>/mcp.
	ToolsBase string
	Backups   int // database copies kept per workspace; 0 = 5
	Log       *slog.Logger
}

// Service runs the workspaces.
type Service struct {
	cfg Config
	reg *registry
	ctx context.Context

	mu   sync.Mutex
	open map[string]*opening // by workspace id
}

type opening struct {
	ready chan struct{} // closed once rt or err is set
	rt    *workspace.Runtime
	err   error
}

// New opens the service's registry. Start opens its workspaces.
func New(cfg Config) (*Service, error) {
	if cfg.Scheme == "" {
		cfg.Scheme = "https"
	}
	if cfg.Backups == 0 {
		cfg.Backups = 5
	}
	cfg.Domain = strings.ToLower(strings.TrimSuffix(cfg.Domain, "."))
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "workspaces"), 0o700); err != nil {
		return nil, err
	}
	reg, err := openRegistry(filepath.Join(cfg.DataDir, "cloud.db"), cfg.MasterKey)
	if err != nil {
		return nil, err
	}
	return &Service{cfg: cfg, reg: reg, open: map[string]*opening{}}, nil
}

// AppURL is the service's own address.
func (s *Service) AppURL() string { return s.URL("app") }

// URL is a workspace's address.
func (s *Service) URL(slug string) string {
	u := s.cfg.Scheme + "://" + slug + "." + s.cfg.Domain
	if s.cfg.Port != "" {
		u += ":" + s.cfg.Port
	}
	return u
}

// Start opens every active workspace in the background, a few at a time,
// so their schedules run and their migrations happen now rather than on
// someone's next visit.
func (s *Service) Start(ctx context.Context) error {
	s.ctx = ctx
	list, err := s.reg.list(ctx)
	if err != nil {
		return err
	}
	go func() {
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for _, w := range list {
			if w.Status != StatusActive {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer func() { <-sem; wg.Done() }()
				if _, err := s.runtime(ctx, w); err != nil {
					s.cfg.Log.Error("cloud: a workspace failed to open", "workspace", w.Slug, "err", err)
				}
			}()
		}
		wg.Wait()
		s.cfg.Log.Info("cloud: workspaces open", "count", len(list))
	}()
	return nil
}

// Close stops every open workspace, giving running prompts up to grace.
func (s *Service) Close(grace time.Duration) error {
	s.mu.Lock()
	open := s.open
	s.open = map[string]*opening{}
	s.mu.Unlock()
	var errs []error
	for _, o := range open {
		<-o.ready
		if o.rt != nil {
			errs = append(errs, o.rt.Close(grace))
		}
	}
	return errors.Join(append(errs, s.reg.Close())...)
}

// Create makes a workspace and returns the link that claims it.
func (s *Service) Create(ctx context.Context, slug, name string) (Workspace, string, error) {
	w, code, err := s.reg.create(ctx, slug, name)
	if err != nil {
		return w, "", err
	}
	return w, s.claimLink(w, code), nil
}

func (s *Service) claimLink(w Workspace, code string) string {
	return s.URL(w.Slug) + "/#/claim/" + code
}

// Workspaces lists every workspace.
func (s *Service) Workspaces(ctx context.Context) ([]Workspace, error) { return s.reg.list(ctx) }

// dir is a workspace's directory.
func (s *Service) dir(w Workspace) string { return filepath.Join(s.cfg.DataDir, "workspaces", w.ID) }

// runtime returns a workspace's running runtime, opening it first if it
// isn't: one opening at a time per workspace, which callers share.
func (s *Service) runtime(ctx context.Context, w Workspace) (*workspace.Runtime, error) {
	s.mu.Lock()
	o, ok := s.open[w.ID]
	if !ok {
		o = &opening{ready: make(chan struct{})}
		s.open[w.ID] = o
		s.mu.Unlock()
		o.rt, o.err = s.start(w)
		if o.err != nil { // the next request tries again
			s.mu.Lock()
			delete(s.open, w.ID)
			s.mu.Unlock()
		}
		close(o.ready)
	} else {
		s.mu.Unlock()
	}
	select {
	case <-o.ready:
		return o.rt, o.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// start opens a workspace: its database, backed up first when migrations
// are pending, and everything that runs on it.
func (s *Service) start(w Workspace) (*workspace.Runtime, error) {
	key, err := s.reg.dataKey(w)
	if err != nil {
		return nil, fmt.Errorf("unsealing its key: %w", err)
	}
	dir := s.dir(w)
	if err := os.MkdirAll(filepath.Join(dir, "backups"), 0o700); err != nil {
		return nil, err
	}
	box, err := s.cfg.Sandboxes(w.Slug)
	if err != nil {
		return nil, err
	}
	_, statErr := os.Stat(workspace.Path(dir))
	fresh, applied := errors.Is(statErr, os.ErrNotExist), false
	cfg := s.cfg.Workspace
	cfg.Dir, cfg.Key, cfg.Box = dir, key, box
	cfg.SandboxPrefix = "beethoven-" + w.Slug + "-"
	cfg.ToolsURL = s.cfg.ToolsBase + "/ws/" + w.Slug + "/mcp"
	cfg.GitHubToken, cfg.NoToken, cfg.GitHubState = "", true, w.Slug+"~install"
	cfg.Env = nil // agents use the workspace's own key, never the operator's
	cfg.BeforeMigrate = func(pending []string, backup func(string) error) error {
		if fresh {
			applied = true
			return nil // a new database: nothing to keep
		}
		name := fmt.Sprintf("%s-before-%s.db", time.Now().UTC().Format("20060102T150405Z"), strings.TrimSuffix(pending[0], ".sql"))
		if err := backup(filepath.Join(dir, "backups", name)); err != nil {
			return fmt.Errorf("backing up before migrating: %w", err)
		}
		s.prune(filepath.Join(dir, "backups"))
		applied = true
		return nil
	}
	a := &cfg.API
	a.SignIn, a.Token, a.Cloud = true, "", true
	a.Hosts = []string{w.Slug + "." + s.cfg.Domain}
	a.CallbackBase, a.StatePrefix = s.AppURL(), w.Slug+"~"
	a.SetupClaim = func(ctx context.Context, code string) bool { return s.reg.claim(ctx, w.ID, code) }
	a.SlackInstalled = func(ctx context.Context, team string) error { return s.reg.route(ctx, routeSlack, team, w.ID) }
	a.SlackUninstalled = func(ctx context.Context, team string) { s.reg.unroute(ctx, routeSlack, team, w.ID) }
	rt, err := workspace.Open(s.base(), cfg)
	if err != nil {
		s.reg.migrated(context.Background(), w.ID, nil, false, err)
		return nil, err
	}
	names, _ := rt.DB.Migrations(context.Background())
	s.reg.migrated(context.Background(), w.ID, names, applied, nil)
	return rt, nil
}

// base is the context workspaces run under: the service's, once started.
func (s *Service) base() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

// prune keeps the newest backups.
func (s *Service) prune(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".db") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // timestamped: oldest first
	for len(names) > s.cfg.Backups {
		os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}

// Retry closes a workspace, if it's open, and opens it again: after a
// migration failed, or to pick up a status change.
func (s *Service) Retry(ctx context.Context, id string) error {
	s.stop(id)
	w, err := s.reg.workspace(ctx, id)
	if err != nil {
		return err
	}
	if w.Status != StatusActive {
		return nil
	}
	_, err = s.runtime(ctx, w)
	return err
}

// SetStatus suspends or reactivates a workspace. A suspended one is closed
// and refuses requests.
func (s *Service) SetStatus(ctx context.Context, id, status string) error {
	if err := s.reg.setStatus(ctx, id, status); err != nil {
		return err
	}
	return s.Retry(ctx, id)
}

// Reclaim issues a new claim link for a workspace.
func (s *Service) Reclaim(ctx context.Context, id string) (string, error) {
	w, err := s.reg.workspace(ctx, id)
	if err != nil {
		return "", err
	}
	if w.Claimed {
		return "", fmt.Errorf("%w: %s has been claimed; its people invite others", store.ErrInvalid, w.Slug)
	}
	code, err := s.reg.reclaim(ctx, id)
	return s.claimLink(w, code), err
}

func (s *Service) stop(id string) {
	s.mu.Lock()
	o, ok := s.open[id]
	delete(s.open, id)
	s.mu.Unlock()
	if ok {
		<-o.ready
		if o.rt != nil {
			o.rt.Close(15 * time.Second)
		}
	}
}

// ---------------------------------------------------------------- routing

// ServeHTTP sends each request where it belongs.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		io.WriteString(w, "ok\n")
		return
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, "/ws/"); ok { // a sandbox calling its workspace's tools
		slug, path, _ := strings.Cut(rest, "/")
		if path != "mcp" {
			http.NotFound(w, r)
			return
		}
		s.toWorkspace(w, r, slug, "/mcp")
		return
	}
	host := hostname(r.Host)
	switch {
	case host == "app."+s.cfg.Domain || host == s.cfg.Domain:
		s.central(w, r)
	case strings.HasSuffix(host, "."+s.cfg.Domain) && !strings.Contains(strings.TrimSuffix(host, "."+s.cfg.Domain), "."):
		s.toWorkspace(w, r, strings.TrimSuffix(host, "."+s.cfg.Domain), "")
	default:
		http.Error(w, "unknown host", http.StatusNotFound)
	}
}

// toWorkspace passes a request to a workspace, with path in place of its
// own if set.
func (s *Service) toWorkspace(w http.ResponseWriter, r *http.Request, slug, path string) {
	ws, err := s.reg.bySlug(r.Context(), slug)
	if err != nil {
		page(w, http.StatusNotFound, "No such workspace", "There's no Beethoven workspace at this address.")
		return
	}
	s.serveWorkspace(w, r, ws, path)
}

func (s *Service) serveWorkspace(w http.ResponseWriter, r *http.Request, ws Workspace, path string) {
	if ws.Status != StatusActive {
		page(w, http.StatusForbidden, "Workspace suspended", "This workspace is suspended. Its owner can contact support to restore it.")
		return
	}
	rt, err := s.runtime(r.Context(), ws)
	if err != nil {
		s.cfg.Log.Error("cloud: opening a workspace", "workspace", ws.Slug, "err", err)
		page(w, http.StatusServiceUnavailable, "Workspace unavailable",
			"This workspace can't be opened right now. We've been told, and are on it.")
		return
	}
	if path != "" {
		r2 := r.Clone(r.Context())
		r2.URL.Path, r2.URL.RawPath = path, ""
		r = r2
	}
	rt.Handler().ServeHTTP(w, r)
}

// central serves app.<domain>: the service's own pages, and the callbacks
// GitHub and Slack make for every workspace.
func (s *Service) central(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/github/authorized" || r.URL.Path == "/github/installed" || r.URL.Path == "/slack/installed":
		s.returnBrowser(w, r)
	case r.URL.Path == "/slack/events" && r.Method == http.MethodPost:
		s.slackEvent(w, r)
	case r.URL.Path == "/slack/interactions" && r.Method == http.MethodPost:
		s.slackInteraction(w, r)
	case r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/"):
		s.admin(w, r)
	case r.URL.Path == "/":
		page(w, http.StatusOK, "Beethoven", "Background coding agents for your team. Sign-up is coming soon.")
	default:
		http.NotFound(w, r)
	}
}

// returnBrowser sends a browser GitHub or Slack returned on to the
// workspace its state names, with everything they sent.
func (s *Service) returnBrowser(w http.ResponseWriter, r *http.Request) {
	slug, _, ok := strings.Cut(r.URL.Query().Get("state"), "~")
	if !ok || CheckSlug(slug) != nil {
		page(w, http.StatusBadRequest, "Back to Beethoven", "Open your workspace's address and try again from there.")
		return
	}
	if _, err := s.reg.bySlug(r.Context(), slug); err != nil {
		page(w, http.StatusNotFound, "No such workspace", "There's no Beethoven workspace for this link.")
		return
	}
	http.Redirect(w, r, s.URL(slug)+r.URL.Path+"?"+r.URL.RawQuery, http.StatusFound)
}

// slackEvent passes an event to the workspace that added its Slack
// workspace, after checking Slack sent it.
func (s *Service) slackEvent(w http.ResponseWriter, r *http.Request) {
	body, ok := s.slackBody(w, r)
	if !ok {
		return
	}
	var env struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		TeamID    string `json:"team_id"`
	}
	json.Unmarshal(body, &env)
	if env.Type == "url_verification" {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, env.Challenge)
		return
	}
	s.toSlackTeam(w, r, body, env.TeamID, func(rt *workspace.Runtime) http.HandlerFunc { return rt.Bot.Events })
}

// slackInteraction passes a click to the workspace that added its Slack
// workspace.
func (s *Service) slackInteraction(w http.ResponseWriter, r *http.Request) {
	body, ok := s.slackBody(w, r)
	if !ok {
		return
	}
	form, _ := url.ParseQuery(string(body))
	var p struct {
		Team struct {
			ID string `json:"id"`
		} `json:"team"`
	}
	json.Unmarshal([]byte(form.Get("payload")), &p)
	s.toSlackTeam(w, r, body, p.Team.ID, func(rt *workspace.Runtime) http.HandlerFunc { return rt.Bot.Interactions })
}

func (s *Service) slackBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	if !slack.Verify(s.cfg.Workspace.SlackApp, r.Header, body) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return nil, false
	}
	return body, true
}

func (s *Service) toSlackTeam(w http.ResponseWriter, r *http.Request, body []byte, team string, handler func(*workspace.Runtime) http.HandlerFunc) {
	ws, err := s.reg.routed(r.Context(), routeSlack, team)
	if err != nil || ws.Status != StatusActive {
		w.WriteHeader(http.StatusOK) // acknowledged, so Slack doesn't retry: no workspace takes it
		return
	}
	rt, err := s.runtime(r.Context(), ws)
	if err != nil {
		http.Error(w, "workspace unavailable", http.StatusServiceUnavailable) // Slack retries
		return
	}
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader(body))
	handler(rt)(w, r2)
}

// hostname is a Host header without its port, in lowercase.
func hostname(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	return strings.ToLower(host)
}
