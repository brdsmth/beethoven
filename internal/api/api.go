// Package api is the HTTP surface: a JSON API for clients, server-sent
// event streams for live timelines, webhook ingress, the agents' MCP tool
// server and the embedded web UI.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brdsmth/beethoven/internal/access"
	"github.com/brdsmth/beethoven/internal/automation"
	"github.com/brdsmth/beethoven/internal/event"
	"github.com/brdsmth/beethoven/internal/github"
	"github.com/brdsmth/beethoven/internal/insights"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/slack"
	"github.com/brdsmth/beethoven/internal/store"
	"github.com/brdsmth/beethoven/internal/tools"
)

type Server struct {
	M        *session.Manager
	Auto     *automation.Service
	GitHub   *github.Client
	Slack    *slack.Client // optional, with SlackBot
	SlackBot *slack.Bot
	SignIn   bool     // require signing in: for servers reachable beyond this machine
	Token    string   // optional: accepted instead of signing in (scripts, the CLI); setting it requires signing in
	Hosts    []string // Host header values accepted besides loopback; "*" accepts any
	UI       fs.FS
	Log      *slog.Logger
	Version  string

	flows flows    // GitHub round trips in progress
	tries throttle // failed sign-ins
}

type Info struct {
	Version   string       `json:"version"`
	Author    store.Author `json:"author"`
	GitHub    bool         `json:"github"`    // the server can reach GitHub: a connected app or a token
	Anthropic bool         `json:"anthropic"` // a model credential reaches sandboxes
	Model     string       `json:"model"`     // the global default; "" = the agent's own
	Effort    string       `json:"effort"`
	Efforts   []string     `json:"efforts"`
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	eff, err := s.M.Effective(r.Context(), "")
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, Info{Version: s.Version, Author: eff.Author, GitHub: s.GitHub.Mode(r.Context()) != "",
		Anthropic: s.anthropicSource(r.Context()) != "", Model: eff.Model, Effort: eff.Effort, Efforts: session.Efforts})
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	api := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.guard(h)) }
	open := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.checked(h)) }

	open("GET /api/auth", s.authState)
	open("POST /api/auth/setup", s.setup)
	open("POST /api/auth/login", s.login)
	open("POST /api/auth/logout", s.logout)
	open("GET /api/auth/invite", s.inviteInfo)
	open("POST /api/auth/invite", s.acceptInvite)
	api("GET /api/info", s.info)
	api("GET /api/sessions", s.listSessions)
	api("POST /api/sessions", s.createSession)
	api("GET /api/sessions/{id}", s.getSession)
	api("POST /api/sessions/{id}/prompts", s.postPrompt)
	api("DELETE /api/sessions/{id}/prompts/{pid}", s.cancelPrompt)
	api("POST /api/sessions/{id}/stop", s.stop)
	api("POST /api/sessions/{id}/archive", s.archive)
	api("POST /api/sessions/{id}/pr", s.openPR)
	api("PUT /api/sessions/{id}/visibility", s.setVisibility)
	api("GET /api/sessions/{id}/attachments/{aid}", s.attachment)
	api("GET /api/sessions/{id}/artifacts", s.artifacts)
	api("GET /api/sessions/{id}/artifacts/{aid}", s.artifact)
	api("POST /api/sessions/{id}/capabilities", s.addCapability)
	api("GET /api/sessions/{id}/events", s.sessionEvents)
	api("GET /api/events", s.allEvents)
	api("GET /api/capabilities", s.listCapabilities)

	api("PUT /api/me", s.updateMe)
	api("GET /api/tokens", s.listTokens)
	api("POST /api/tokens", s.createToken)
	api("DELETE /api/tokens/{id}", s.deleteToken)
	api("GET /api/people", s.listPeople)
	api("PUT /api/people/{id}", s.updatePerson)
	api("GET /api/invites", s.listInvites)
	api("POST /api/invites", s.createInvite)
	api("DELETE /api/invites/{id}", s.deleteInvite)
	api("GET /api/teams", s.listTeams)
	api("POST /api/teams", s.createTeam)
	api("GET /api/teams/{id}", s.getTeam)
	api("PUT /api/teams/{id}", s.updateTeam)
	api("POST /api/teams/{id}/archive", s.archiveTeam)
	api("POST /api/teams/{id}/join", s.joinTeam)
	api("PUT /api/teams/{id}/members/{account}", s.setMember)
	api("DELETE /api/teams/{id}/members/{account}", s.removeMember)
	api("GET /api/audit", s.auditLog)

	api("GET /api/settings", s.getSettings)
	api("PUT /api/settings", s.putSettings)
	api("GET /api/credentials", s.getCredentials)
	api("PUT /api/credentials", s.putCredential)
	api("GET /api/environments", s.listEnvironments)
	api("POST /api/environments", s.createEnvironment)
	api("GET /api/environments/{id}", s.getEnvironment)
	api("PUT /api/environments/{id}", s.updateEnvironment)
	api("DELETE /api/environments/{id}", s.deleteEnvironment)
	api("GET /api/mcp-servers", s.listMCP)
	api("POST /api/mcp-servers", s.createMCP)
	api("PUT /api/mcp-servers/{id}", s.updateMCP)
	api("DELETE /api/mcp-servers/{id}", s.deleteMCP)
	api("GET /api/github", s.githubStatus)
	api("POST /api/github/app", s.registerApp)
	api("POST /api/github/app/created", s.appCreated)
	api("DELETE /api/github/app", s.forgetApp)
	api("POST /api/github/authorize", s.authorize)
	api("POST /api/github/account", s.connectAccount)
	api("DELETE /api/github/account", s.disconnectAccount)
	api("GET /api/insights", s.insights)
	api("GET /api/repos", s.listRepos)
	api("GET /api/repos/{owner}/{name}/branches", s.listBranches)
	api("GET /api/projects/options", s.projectOptions)
	api("POST /api/projects", s.createProject)

	api("GET /api/automations", s.listAutomations)
	api("GET /api/automations/{id}", s.getAutomation)
	api("POST /api/automations", s.createAutomation)
	api("PUT /api/automations/{id}", s.updateAutomation)
	api("DELETE /api/automations/{id}", s.deleteAutomation)
	api("POST /api/automations/{id}/run", s.runAutomation)
	api("POST /api/automations/{id}/hook-token", s.newHookToken)
	api("GET /api/automations/{id}/runs", s.automationRuns)

	api("GET /api/secrets", s.listSecrets)
	api("PUT /api/secrets", s.putSecret)
	api("POST /api/secrets/import", s.importSecrets)
	api("GET /api/secrets/effective", s.effectiveSecrets)
	api("DELETE /api/secrets", s.deleteSecret)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok\n") })
	mux.HandleFunc("POST /hooks/{id}", s.hook)
	mux.HandleFunc("POST /webhooks/github", s.githubWebhook) // signed with the app's webhook secret
	api("GET /api/automations/events", s.eventKinds)
	mux.HandleFunc("GET /github/{step}", returnTo("github"))
	if s.Slack != nil && s.SlackBot != nil {
		api("GET /api/slack", s.slackStatus)
		api("POST /api/slack/app", s.createSlackApp)
		api("DELETE /api/slack/app", s.forgetSlackApp)
		api("POST /api/slack/install", s.installSlack)
		api("POST /api/slack/installed", s.slackInstalled)
		api("DELETE /api/slack/installations/{team}", s.uninstallSlack)
		api("DELETE /api/slack/channels/{team}/{channel}", s.deleteSlackChannel)
		api("PUT /api/slack/channels/{team}/{channel}/team", s.bindSlackChannel)
		mux.HandleFunc("POST /slack/events", s.SlackBot.Events) // signed by Slack
		mux.HandleFunc("POST /slack/interactions", s.SlackBot.Interactions)
		mux.HandleFunc("GET /slack/{step}", returnTo("slack"))
	}
	mux.Handle("/mcp", tools.Handler(s.M))
	api("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, http.StatusNotFound, "no such endpoint") })
	mux.Handle("/", s.static())
	return mux
}

// guard protects the API: requests must pass checked and, when the server
// asks for it, come from a signed-in browser or carry the token.
func (s *Server) guard(h http.Handler) http.Handler {
	return s.checked(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, err := s.actorFor(r)
		if errors.Is(err, errSignIn) {
			fail(w, http.StatusUnauthorized, "sign in required")
			return
		}
		if err != nil {
			replyErr(w, err)
			return
		}
		h.ServeHTTP(w, r.WithContext(access.With(r.Context(), who)))
	}))
}

// checked refuses cross-site requests (CSRF) and unexpected Host headers
// (DNS rebinding), for a server that often listens on localhost.
func (s *Server) checked(h http.Handler) http.Handler {
	return http.NewCrossOriginProtection().Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			fail(w, http.StatusForbidden, "host not allowed: "+r.Host)
			return
		}
		h.ServeHTTP(w, r)
	}))
}

func (s *Server) hostAllowed(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	if host == "localhost" || net.ParseIP(host).IsLoopback() {
		return true
	}
	for _, h := range s.Hosts {
		if h == "*" || strings.EqualFold(h, host) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- sessions

// listSessions lists the sessions the viewer may read, newest activity
// first. ?team= narrows to teams (comma separated; "workspace" for the
// workspace's own), ?mine=1 to the viewer's own, ?q= to titles containing
// it. ?archived=1 lists archived sessions, ?archived=all both.
func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	who := actor(r)
	limit, _ := strconv.Atoi(q.Get("limit"))
	viewer := who.Viewer()
	f := store.SessionFilter{ParentID: q.Get("parent"), Query: strings.TrimSpace(q.Get("q")), Limit: limit, Viewer: &viewer}
	if v := q.Get("archived"); v != "all" {
		archived := v == "1"
		f.Archived = &archived
	}
	if v := q.Get("team"); v != "" {
		f.Teams = []string{}
		for _, t := range strings.Split(v, ",") {
			if t == "workspace" {
				t = ""
			}
			f.Teams = append(f.Teams, t)
		}
	}
	if q.Get("mine") == "1" {
		f.Account = firstOf(who.AccountID, "-")
	}
	list, err := s.M.Store().Sessions(r.Context(), f)
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, orEmpty(list))
}

// session loads the session a request names, if the viewer may read it,
// and checks they may do what may asks; otherwise it replies.
func (s *Server) session(w http.ResponseWriter, r *http.Request, may func(access.Actor, store.Session) bool, what string) (store.Session, bool) {
	sess, err := s.M.Session(r.Context(), r.PathValue("id"))
	if err != nil {
		replyErr(w, err)
		return sess, false
	}
	who := actor(r)
	if !access.ReadSession(who, sess) {
		hidden(w)
		return sess, false
	}
	if may != nil && !may(who, sess) {
		if sess.TeamID != "" && !who.Member(sess.TeamID) {
			what += ": it belongs to a team you're not in"
		}
		forbid(w, what)
		return sess, false
	}
	return sess, true
}

// target checks the viewer may start work owned by teamID in environmentID
// (either may be empty), and returns the team the work will belong to.
func (s *Server) target(w http.ResponseWriter, r *http.Request, teamID, environmentID string) (string, bool) {
	who := actor(r)
	if environmentID != "" {
		e, err := s.M.Store().Environment(r.Context(), environmentID)
		if err != nil || !access.UseEnvironment(who, e) {
			fail(w, http.StatusBadRequest, "no such project")
			return "", false
		}
		teamID = firstOf(teamID, e.TeamID)
	}
	if teamID == "" {
		return "", true
	}
	t, err := s.M.Store().Team(r.Context(), teamID)
	switch {
	case err != nil:
		fail(w, http.StatusBadRequest, "no such team")
		return "", false
	case !access.UseTeam(who, teamID):
		forbid(w, "start work for "+t.Name+": you're not in it")
		return "", false
	case t.ArchivedAt != nil:
		fail(w, http.StatusConflict, t.Name+" is archived")
		return "", false
	}
	return teamID, true
}

// accountOf is the person a request's work is for: the viewer, unless the
// server itself is acting.
func accountOf(r *http.Request) string {
	if who := actor(r); who.IsPerson() {
		return who.AccountID
	}
	return ""
}

// createReq targets an environment, repositories (repo/base_branch is a
// shorthand for one), or neither for a scratch workspace. Sent as a
// multipart form, it can attach files (see decodeWithFiles).
type createReq struct {
	Prompt        string          `json:"prompt"`
	EnvironmentID string          `json:"environment_id"`
	Repos         []store.RepoRef `json:"repos"`
	Repo          string          `json:"repo"`
	BaseBranch    string          `json:"base_branch"`
	Model         string          `json:"model"`
	Effort        string          `json:"effort"`
	Title         string          `json:"title"`
	Source        string          `json:"source"`
	TeamID        string          `json:"team_id"`    // the owning team; a team project's by default
	Visibility    string          `json:"visibility"` // workspace, team or private; the team's default otherwise
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var in createReq
	files, ok := decodeWithFiles(w, r, &in)
	if !ok {
		return
	}
	if in.Source != "cli" {
		in.Source = "web"
	}
	if strings.TrimSpace(in.Repo) != "" {
		in.Repos = append([]store.RepoRef{{Repo: in.Repo, BaseBranch: in.BaseBranch}}, in.Repos...)
	}
	team, ok := s.target(w, r, in.TeamID, in.EnvironmentID)
	if !ok {
		return
	}
	sess, p, err := s.M.Submit(r.Context(), session.Request{Prompt: in.Prompt, Attachments: files,
		EnvironmentID: in.EnvironmentID, Repos: in.Repos, Model: in.Model, Effort: in.Effort, Title: in.Title,
		Author: s.author(r), AccountID: accountOf(r), TeamID: team, Visibility: in.Visibility, Source: in.Source})
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusCreated, map[string]any{"session": sess, "prompt": p})
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w, r, nil, "")
	if !ok {
		return
	}
	prompts, err := s.M.Store().Prompts(r.Context(), sess.ID)
	if err != nil {
		replyErr(w, err)
		return
	}
	pulls, err := s.M.Store().SessionPullRequests(r.Context(), sess.ID)
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"session": sess, "prompts": orEmpty(prompts), "pull_requests": orEmpty(pulls)})
}

// insights measures the last ?days calendar days (default 30) in the
// viewer's ?tz.
func (s *Server) insights(w http.ResponseWriter, r *http.Request) {
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil {
		days = 30
	}
	if days < 1 || days > 366 {
		fail(w, http.StatusBadRequest, "days must be between 1 and 366")
		return
	}
	loc, err := time.LoadLocation(r.URL.Query().Get("tz"))
	if err != nil {
		loc = time.UTC
	}
	who, team := actor(r), r.URL.Query().Get("team") // "" = all, "workspace" = the workspace's own
	keep := func(sess store.Session) bool {
		return access.ReadSession(who, sess) && (team == "" || sess.TeamID == strings.TrimPrefix(team, "workspace"))
	}
	report, err := insights.Compute(r.Context(), s.M.Store(), days, time.Now(), loc, keep)
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, report)
}

func (s *Server) postPrompt(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Content string `json:"content"`
		Model   string `json:"model"`
		Effort  string `json:"effort"`
		Source  string `json:"source"`
	}
	if _, ok := s.session(w, r, access.PromptSession, "prompt this session"); !ok {
		return
	}
	files, ok := decodeWithFiles(w, r, &in)
	if !ok {
		return
	}
	if in.Source != "cli" {
		in.Source = "web"
	}
	p, err := s.M.Enqueue(r.Context(), r.PathValue("id"), session.PromptInput{Content: in.Content, Attachments: files,
		Model: in.Model, Effort: in.Effort, Author: s.author(r), AccountID: accountOf(r), Source: in.Source})
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusCreated, p)
}

func (s *Server) cancelPrompt(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.session(w, r, access.PromptSession, "cancel prompts here"); !ok {
		return
	}
	if err := s.M.CancelPrompt(r.Context(), r.PathValue("id"), r.PathValue("pid")); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.session(w, r, access.PromptSession, "stop this session"); !ok {
		return
	}
	if err := s.M.Stop(r.Context(), r.PathValue("id")); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) archive(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Archived bool `json:"archived"`
	}
	if !decode(w, r, &in) {
		return
	}
	if _, ok := s.session(w, r, access.ManageSession, "archive this session"); !ok {
		return
	}
	if err := s.M.Archive(r.Context(), r.PathValue("id"), in.Archived); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) openPR(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Repo  string `json:"repo"`
		Title string `json:"title"`
		Body  string `json:"body"`
		Draft *bool  `json:"draft"`
	}
	if !decode(w, r, &in) {
		return
	}
	if _, ok := s.session(w, r, access.PromptSession, "open pull requests for this session"); !ok {
		return
	}
	url, err := s.M.OpenPR(r.Context(), r.PathValue("id"), in.Repo, in.Title, in.Body, in.Draft)
	warning := ""
	if session.IsUncommittedWarning(err) {
		warning, err = err.Error(), nil
	}
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]string{"url": url, "warning": warning})
}

// setVisibility changes who may read a session.
func (s *Server) setVisibility(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Visibility string `json:"visibility"`
	}
	if !decode(w, r, &in) {
		return
	}
	before, ok := s.session(w, r, access.ManageSession, "change who sees this session")
	if !ok {
		return
	}
	if in.Visibility == store.VisibilityPrivate && before.AccountID != accountOf(r) {
		fail(w, http.StatusBadRequest, "only the person who started a session can make it private")
		return
	}
	sess, err := s.M.SetVisibility(r.Context(), before.ID, in.Visibility)
	if err != nil {
		replyErr(w, err)
		return
	}
	s.audit(r, sess.TeamID, "session.visibility_changed", sess.ID,
		map[string]string{"title": sess.Title, "from": before.Visibility, "to": sess.Visibility})
	reply(w, http.StatusOK, sess)
}

// ------------------------------------------------------------------ streams

// sessionEvents streams a session's timeline as server-sent events: the
// current session state, then the stored log after the client's last seen
// sequence (Last-Event-ID or ?after=), then live events. ?format=json
// returns the stored log as a JSON array instead.
func (s *Server) sessionEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.session(w, r, nil, "")
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	if v := r.URL.Query().Get("after"); v != "" && after == 0 {
		after, _ = strconv.ParseInt(v, 10, 64)
	}
	if r.URL.Query().Get("format") == "json" {
		evs, err := s.M.Store().Events(r.Context(), id, after, 10000)
		if err != nil {
			replyErr(w, err)
			return
		}
		reply(w, http.StatusOK, orEmpty(evs))
		return
	}

	sub := s.M.Hub().Subscribe(id) // before replay, so nothing falls in between
	defer s.M.Hub().Unsubscribe(sub)
	stream := startStream(w)
	stream.send(store.Event{Session: id, Type: session.EvSession, Data: mustJSON(sess), At: time.Now()})
	for {
		evs, err := s.M.Store().Events(r.Context(), id, after, 1000)
		if err != nil {
			return
		}
		for _, e := range evs {
			stream.send(e)
			after = e.Seq
		}
		if len(evs) < 1000 {
			break
		}
	}
	stream.flush()
	stream.follow(r, sub, func(e store.Event) (store.Event, bool) {
		if e.Seq != 0 && e.Seq <= after {
			return e, false // already replayed
		}
		if e.Seq != 0 {
			after = e.Seq
		}
		return e, true
	})
}

// EvHidden tells a live list that a session it showed is no longer the
// viewer's to see.
const EvHidden = "session_hidden"

// allEvents streams state changes of the sessions the viewer may read, for
// live lists. Their teams are looked up again every minute.
func (s *Server) allEvents(w http.ResponseWriter, r *http.Request) {
	sub := s.M.Hub().Subscribe(session.AllSessions)
	defer s.M.Hub().Unsubscribe(sub)
	stream := startStream(w)
	stream.flush()
	who, checked := actor(r), time.Now()
	shown := map[string]bool{}
	stream.follow(r, sub, func(e store.Event) (store.Event, bool) {
		if time.Since(checked) > time.Minute {
			if again, err := s.actorFor(r); err == nil {
				who = again
			}
			checked = time.Now()
		}
		var sess store.Session
		if json.Unmarshal(e.Data, &sess) != nil || sess.ID == "" {
			return e, false
		}
		if access.ReadSession(who, sess) {
			shown[sess.ID] = true
			return e, true
		}
		if !shown[sess.ID] {
			return e, false
		}
		// Hidden since: tell the list that showed it, without its details.
		delete(shown, sess.ID)
		gone := store.Event{Session: sess.ID, Type: EvHidden, Data: mustJSON(map[string]string{"id": sess.ID}), At: e.At}
		return gone, true
	})
}

type sseStream struct {
	w http.ResponseWriter
	f http.Flusher
}

func startStream(w http.ResponseWriter) *sseStream {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	f, _ := w.(http.Flusher)
	return &sseStream{w: w, f: f}
}

func (s *sseStream) send(e store.Event) {
	if e.Seq != 0 {
		fmt.Fprintf(s.w, "id: %d\n", e.Seq)
	}
	b, _ := json.Marshal(e)
	fmt.Fprintf(s.w, "data: %s\n\n", b)
}

func (s *sseStream) flush() {
	if s.f != nil {
		s.f.Flush()
	}
}

// follow forwards live events until the client leaves or the hub drops a
// slow subscriber; either way the client reconnects with Last-Event-ID.
func (s *sseStream) follow(r *http.Request, sub *session.Sub, keep func(store.Event) (store.Event, bool)) {
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			if e, ok := keep(e); ok {
				s.send(e)
				s.flush()
			}
		case <-ping.C:
			io.WriteString(s.w, ": ping\n\n")
			s.flush()
		case <-r.Context().Done():
			return
		}
	}
}

// -------------------------------------------------------------- automations

// listAutomations lists the automations the viewer may see.
func (s *Server) listAutomations(w http.ResponseWriter, r *http.Request) {
	list, err := s.M.Store().Automations(r.Context())
	if err != nil {
		replyErr(w, err)
		return
	}
	who := actor(r)
	out := []store.Automation{}
	for _, a := range list {
		if access.UseAutomation(who, a) {
			out = append(out, a)
		}
	}
	reply(w, http.StatusOK, out)
}

// automation loads the automation a request names, if the viewer may see
// it, and checks they may manage it when manage is set.
func (s *Server) automation(w http.ResponseWriter, r *http.Request, manage bool) (store.Automation, bool) {
	a, err := s.M.Store().Automation(r.Context(), r.PathValue("id"))
	if err != nil {
		replyErr(w, err)
		return a, false
	}
	who := actor(r)
	if !access.UseAutomation(who, a) {
		hidden(w)
		return a, false
	}
	if manage && !access.ManageAutomation(who, a) {
		forbid(w, "change this automation")
		return a, false
	}
	return a, true
}

func (s *Server) getAutomation(w http.ResponseWriter, r *http.Request) {
	if a, ok := s.automation(w, r, false); ok {
		reply(w, http.StatusOK, a)
	}
}

func (s *Server) createAutomation(w http.ResponseWriter, r *http.Request) {
	var spec automation.Spec
	if !decode(w, r, &spec) {
		return
	}
	team, ok := s.target(w, r, spec.TeamID, spec.EnvironmentID)
	if !ok {
		return
	}
	spec.TeamID = team
	a, token, err := s.Auto.Create(r.Context(), spec, accountOf(r))
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusCreated, map[string]any{"automation": a, "hook_token": token, "hook_path": "/hooks/" + a.ID})
}

func (s *Server) updateAutomation(w http.ResponseWriter, r *http.Request) {
	var spec automation.Spec
	if !decode(w, r, &spec) {
		return
	}
	before, ok := s.automation(w, r, true)
	if !ok {
		return
	}
	if spec.TeamID != "" && spec.TeamID != before.TeamID {
		fail(w, http.StatusBadRequest, "an automation's team can't change")
		return
	}
	if _, ok := s.target(w, r, before.TeamID, spec.EnvironmentID); !ok {
		return
	}
	a, err := s.Auto.Update(r.Context(), r.PathValue("id"), spec)
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, a)
}

func (s *Server) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.automation(w, r, true); !ok {
		return
	}
	if err := s.M.Store().DeleteAutomation(r.Context(), r.PathValue("id")); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) runAutomation(w http.ResponseWriter, r *http.Request) {
	a, ok := s.automation(w, r, true)
	if !ok {
		return
	}
	a.Enabled = true // a manual run works on paused automations too
	sess, p, err := s.Auto.Fire(r.Context(), a, "manual", "", "")
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusCreated, map[string]any{"session": sess, "prompt": p})
}

// newHookToken replaces an automation's webhook token and returns it, the
// only time it is shown.
func (s *Server) newHookToken(w http.ResponseWriter, r *http.Request) {
	a, ok := s.automation(w, r, true)
	if !ok {
		return
	}
	token, err := s.Auto.NewHookToken(r.Context(), a.ID)
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"hook_token": token, "hook_path": "/hooks/" + a.ID})
}

func (s *Server) automationRuns(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.automation(w, r, false); !ok {
		return
	}
	runs, err := s.M.Store().AutomationRuns(r.Context(), r.PathValue("id"), 50)
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, orEmpty(runs))
}

// hook fires an automation from an external system:
//
//	POST /hooks/{id}  Authorization: Bearer <hook token>  (or ?token=)
//	Idempotency-Key: <optional; repeated deliveries fire once>
//
// The request body (up to 64 KiB) is handed to the agent with the prompt.
func (s *Server) hook(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	a, err := s.Auto.CheckHook(r.Context(), r.PathValue("id"), token)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			err = session.ErrUnauthorized // don't reveal which ids exist
		}
		replyErr(w, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		fail(w, http.StatusRequestEntityTooLarge, "payload too large")
		return
	}
	key := firstOf(r.Header.Get("Idempotency-Key"), r.Header.Get("X-GitHub-Delivery"))
	sess, p, err := s.Auto.Fire(r.Context(), a, "webhook", strings.ToValidUTF8(string(body), "�"), key)
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusAccepted, map[string]string{"session_id": sess.ID, "prompt_id": p.ID})
}

// githubWebhook receives the GitHub App's (or a repository webhook's) event
// deliveries and fires the automations they trigger.
func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	e, err := s.GitHub.ParseWebhook(r)
	switch {
	case errors.Is(err, github.ErrBadSignature):
		fail(w, http.StatusUnauthorized, err.Error())
		return
	case err != nil:
		fail(w, http.StatusBadRequest, err.Error())
		return
	case e == nil:
		reply(w, http.StatusOK, map[string]any{"ignored": true})
		return
	}
	started, err := s.Auto.Dispatch(r.Context(), *e)
	if err != nil {
		s.Log.Warn("github event: some automations didn't run", "kind", e.Kind, "repo", e.Repo, "err", err)
	}
	ids := []string{}
	for _, p := range started {
		ids = append(ids, p.SessionID)
	}
	reply(w, http.StatusAccepted, map[string]any{"event": e.Kind, "sessions": ids})
}

// eventKinds lists the events automations can trigger on.
func (s *Server) eventKinds(w http.ResponseWriter, r *http.Request) {
	reply(w, http.StatusOK, map[string]any{"kinds": event.Kinds,
		"webhook": map[string]any{"path": "/webhooks/github", "secret": s.GitHub.WebhookSecret(r.Context()) != ""}})
}

// ----------------------------------------------------------------------- ui

func (s *Server) static() http.Handler {
	if s.UI != nil {
		if _, err := fs.Stat(s.UI, "index.html"); err == nil {
			return http.FileServerFS(s.UI)
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "Beethoven is running. The web UI was not built into this binary; run `make` to build it.\n")
	})
}

// ------------------------------------------------------------------ helpers

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	reply(w, code, map[string]string{"error": msg})
}

func replyErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, store.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, store.ErrConflict):
		code = http.StatusConflict
	case errors.Is(err, session.ErrInvalid), errors.Is(err, session.ErrDepth):
		code = http.StatusBadRequest
	case errors.Is(err, session.ErrUnauthorized):
		code = http.StatusUnauthorized
	case errors.Is(err, automation.ErrBusy), errors.Is(err, automation.ErrDisabled):
		code = http.StatusConflict
	}
	fail(w, code, err.Error())
}

func orEmpty[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
