// Package api is the HTTP surface: a JSON API for clients, server-sent
// event streams for live timelines, webhook ingress, the agents' MCP tool
// server and the embedded web UI.
package api

import (
	"crypto/subtle"
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

	"github.com/brdsmth/beethoven/internal/automation"
	"github.com/brdsmth/beethoven/internal/github"
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
	Token    string   // optional: required on /api when set
	Hosts    []string // Host header values accepted besides loopback; "*" accepts any
	UI       fs.FS
	Log      *slog.Logger
	Version  string

	flows flows // GitHub round trips in progress
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

	api("GET /api/info", s.info)
	api("GET /api/sessions", s.listSessions)
	api("POST /api/sessions", s.createSession)
	api("GET /api/sessions/{id}", s.getSession)
	api("POST /api/sessions/{id}/prompts", s.postPrompt)
	api("DELETE /api/sessions/{id}/prompts/{pid}", s.cancelPrompt)
	api("POST /api/sessions/{id}/stop", s.stop)
	api("POST /api/sessions/{id}/archive", s.archive)
	api("POST /api/sessions/{id}/pr", s.openPR)
	api("GET /api/sessions/{id}/events", s.sessionEvents)
	api("GET /api/events", s.allEvents)

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
	api("GET /api/repos", s.listRepos)
	api("GET /api/repos/{owner}/{name}/branches", s.listBranches)

	api("GET /api/automations", s.listAutomations)
	api("GET /api/automations/{id}", s.getAutomation)
	api("POST /api/automations", s.createAutomation)
	api("PUT /api/automations/{id}", s.updateAutomation)
	api("DELETE /api/automations/{id}", s.deleteAutomation)
	api("POST /api/automations/{id}/run", s.runAutomation)
	api("GET /api/automations/{id}/runs", s.automationRuns)

	api("GET /api/secrets", s.listSecrets)
	api("PUT /api/secrets", s.putSecret)
	api("POST /api/secrets/import", s.importSecrets)
	api("GET /api/secrets/effective", s.effectiveSecrets)
	api("DELETE /api/secrets", s.deleteSecret)

	mux.HandleFunc("POST /hooks/{id}", s.hook)
	mux.HandleFunc("GET /github/{step}", returnTo("github"))
	if s.Slack != nil && s.SlackBot != nil {
		api("GET /api/slack", s.slackStatus)
		api("POST /api/slack/app", s.createSlackApp)
		api("DELETE /api/slack/app", s.forgetSlackApp)
		api("POST /api/slack/install", s.installSlack)
		api("POST /api/slack/installed", s.slackInstalled)
		api("DELETE /api/slack/installations/{team}", s.uninstallSlack)
		api("DELETE /api/slack/channels/{team}/{channel}", s.deleteSlackChannel)
		mux.HandleFunc("POST /slack/events", s.SlackBot.Events) // signed by Slack
		mux.HandleFunc("POST /slack/interactions", s.SlackBot.Interactions)
		mux.HandleFunc("GET /slack/{step}", returnTo("slack"))
	}
	mux.Handle("/mcp", tools.Handler(s.M))
	api("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, http.StatusNotFound, "no such endpoint") })
	mux.Handle("/", s.static())
	return mux
}

// guard protects the API of a server that usually listens on localhost:
// cross-site requests are refused (CSRF), unexpected Host headers are
// refused (DNS rebinding), and the token is checked when one is set.
func (s *Server) guard(h http.Handler) http.Handler {
	return http.NewCrossOriginProtection().Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			fail(w, http.StatusForbidden, "host not allowed: "+r.Host)
			return
		}
		if s.Token != "" && !s.authorized(r) {
			fail(w, http.StatusUnauthorized, "missing or invalid token")
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

func (s *Server) authorized(r *http.Request) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		got = r.URL.Query().Get("token") // EventSource cannot set headers
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1
}

// ---------------------------------------------------------------- sessions

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	archived := q.Get("archived") == "1"
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := s.M.Store().Sessions(r.Context(), store.SessionFilter{Archived: &archived, ParentID: q.Get("parent"), Limit: limit})
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, orEmpty(list))
}

// createReq targets an environment, repositories (repo/base_branch is a
// shorthand for one), or neither for a scratch workspace.
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
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var in createReq
	if !decode(w, r, &in) {
		return
	}
	if in.Source != "cli" {
		in.Source = "web"
	}
	if strings.TrimSpace(in.Repo) != "" {
		in.Repos = append([]store.RepoRef{{Repo: in.Repo, BaseBranch: in.BaseBranch}}, in.Repos...)
	}
	sess, p, err := s.M.Submit(r.Context(), session.Request{Prompt: in.Prompt, EnvironmentID: in.EnvironmentID,
		Repos: in.Repos, Model: in.Model, Effort: in.Effort, Title: in.Title, Author: s.author(r), Source: in.Source})
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusCreated, map[string]any{"session": sess, "prompt": p})
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.M.Session(r.Context(), r.PathValue("id"))
	if err != nil {
		replyErr(w, err)
		return
	}
	prompts, err := s.M.Store().Prompts(r.Context(), sess.ID)
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"session": sess, "prompts": orEmpty(prompts)})
}

func (s *Server) postPrompt(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Content string `json:"content"`
		Model   string `json:"model"`
		Effort  string `json:"effort"`
		Source  string `json:"source"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Source != "cli" {
		in.Source = "web"
	}
	p, err := s.M.Enqueue(r.Context(), r.PathValue("id"), session.PromptInput{Content: in.Content, Model: in.Model,
		Effort: in.Effort, Author: s.author(r), Source: in.Source})
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusCreated, p)
}

func (s *Server) cancelPrompt(w http.ResponseWriter, r *http.Request) {
	if err := s.M.CancelPrompt(r.Context(), r.PathValue("id"), r.PathValue("pid")); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
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

// ------------------------------------------------------------------ streams

// sessionEvents streams a session's timeline as server-sent events: the
// current session state, then the stored log after the client's last seen
// sequence (Last-Event-ID or ?after=), then live events. ?format=json
// returns the stored log as a JSON array instead.
func (s *Server) sessionEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := s.M.Session(r.Context(), id)
	if err != nil {
		replyErr(w, err)
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
	stream.follow(r, sub, func(e store.Event) bool {
		if e.Seq != 0 && e.Seq <= after {
			return false // already replayed
		}
		if e.Seq != 0 {
			after = e.Seq
		}
		return true
	})
}

// allEvents streams state changes of every session, for live lists.
func (s *Server) allEvents(w http.ResponseWriter, r *http.Request) {
	sub := s.M.Hub().Subscribe(session.AllSessions)
	defer s.M.Hub().Unsubscribe(sub)
	stream := startStream(w)
	stream.flush()
	stream.follow(r, sub, func(store.Event) bool { return true })
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
func (s *sseStream) follow(r *http.Request, sub *session.Sub, keep func(store.Event) bool) {
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			if keep(e) {
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

func (s *Server) listAutomations(w http.ResponseWriter, r *http.Request) {
	list, err := s.M.Store().Automations(r.Context())
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, orEmpty(list))
}

func (s *Server) getAutomation(w http.ResponseWriter, r *http.Request) {
	a, err := s.M.Store().Automation(r.Context(), r.PathValue("id"))
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, a)
}

func (s *Server) createAutomation(w http.ResponseWriter, r *http.Request) {
	var spec automation.Spec
	if !decode(w, r, &spec) {
		return
	}
	a, token, err := s.Auto.Create(r.Context(), spec)
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
	a, err := s.Auto.Update(r.Context(), r.PathValue("id"), spec)
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, a)
}

func (s *Server) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	if err := s.M.Store().DeleteAutomation(r.Context(), r.PathValue("id")); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) runAutomation(w http.ResponseWriter, r *http.Request) {
	a, err := s.M.Store().Automation(r.Context(), r.PathValue("id"))
	if err != nil {
		replyErr(w, err)
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

func (s *Server) automationRuns(w http.ResponseWriter, r *http.Request) {
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
