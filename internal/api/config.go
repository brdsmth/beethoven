package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/brdsmth/beethoven/internal/access"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

// author is the commit author for prompts sent through the API: a
// person's own identity; for the server, the settings' author or its
// owner's.
func (s *Server) author(r *http.Request) store.Author {
	return s.M.AuthorFor(r.Context(), accountOf(r))
}

// ----------------------------------------------------------------- settings

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.M.Store().Settings(r.Context())
	if err != nil {
		replyErr(w, err)
		return
	}
	cfg, author := s.M.Config(), s.M.DefaultAuthor(r.Context())
	reply(w, http.StatusOK, map[string]any{
		"settings": settings,
		// What applies where a setting is left empty.
		"defaults": map[string]any{
			"image":         cfg.Image,
			"idle_minutes":  int(cfg.IdleTimeout / time.Minute),
			"turn_minutes":  int(cfg.TurnTimeout / time.Minute),
			"branch_prefix": "beethoven/",
			"author_name":   author.Name,
			"author_email":  author.Email,
		},
		"efforts": session.Efforts,
	})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in store.Settings
	if !decode(w, r, &in) {
		return
	}
	if !actor(r).Admin() {
		forbid(w, "change the settings: ask an admin")
		return
	}
	if (in.AuthorName == "") != (in.AuthorEmail == "") {
		fail(w, http.StatusBadRequest, "set both the author's name and email, or neither")
		return
	}
	if err := session.ValidateSettings(in); err != nil {
		replyErr(w, err)
		return
	}
	if err := s.M.Store().SaveSettings(r.Context(), in); err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, in)
}

// -------------------------------------------------------------- credentials

var credentialNames = []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "GITHUB_TOKEN"}

// anthropicSource says where the agent's credential comes from, if anywhere.
func (s *Server) anthropicSource(ctx context.Context) string {
	creds, _ := s.M.Store().Credentials(ctx)
	for _, k := range session.AgentCredentials {
		if creds[k] != "" {
			return "settings"
		}
	}
	for _, k := range session.AgentCredentials {
		if s.M.Config().Env[k] != "" {
			return "environment"
		}
	}
	if secrets, err := s.M.Store().Secrets(ctx, ptr("")); err == nil {
		for _, x := range secrets {
			if slices.Contains(session.AgentCredentials, x.Name) {
				return "secret"
			}
		}
	}
	return ""
}

// getCredentials reports which credentials are set and where they come
// from, never their values. ?check=1 also asks GitHub whose token it is.
func (s *Server) getCredentials(w http.ResponseWriter, r *http.Request) {
	creds, err := s.M.Store().Credentials(r.Context())
	if err != nil {
		replyErr(w, err)
		return
	}
	stored := map[string]bool{}
	for _, k := range credentialNames {
		stored[k] = creds[k] != ""
	}
	gh := map[string]any{"source": s.GitHub.TokenSource(r.Context())}
	if r.URL.Query().Get("check") == "1" && gh["source"] != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if login, err := s.GitHub.User(ctx); err != nil {
			gh["error"] = err.Error()
		} else {
			gh["login"] = login
		}
	}
	reply(w, http.StatusOK, map[string]any{
		"stored":    stored,
		"anthropic": map[string]string{"source": s.anthropicSource(r.Context())},
		"github":    gh,
	})
}

// putCredential stores one credential; an empty value removes it.
func (s *Server) putCredential(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !actor(r).Admin() {
		forbid(w, "change credentials: ask an admin")
		return
	}
	if !slices.Contains(credentialNames, in.Name) {
		fail(w, http.StatusBadRequest, "credential must be one of "+strings.Join(credentialNames, ", "))
		return
	}
	if err := s.M.Store().SetCredential(r.Context(), in.Name, strings.TrimSpace(in.Value)); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------- environments

type environmentReq struct {
	TeamID         string          `json:"team_id"` // on creation only: a team's project, or the workspace's
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Repos          []store.RepoRef `json:"repos"`
	SetupScript    string          `json:"setup_script"`
	StartScript    string          `json:"start_script"`
	InheritSecrets *bool           `json:"inherit_secrets"` // default true
	Overrides      store.Overrides `json:"overrides"`
}

func (in environmentReq) apply(e *store.Environment) error {
	e.Name, e.Description, e.Repos = in.Name, in.Description, in.Repos
	e.SetupScript, e.StartScript, e.Overrides = in.SetupScript, in.StartScript, in.Overrides
	e.InheritSecrets = in.InheritSecrets == nil || *in.InheritSecrets
	return session.ValidateEnvironment(e)
}

// listEnvironments lists the projects the viewer may use.
func (s *Server) listEnvironments(w http.ResponseWriter, r *http.Request) {
	list, err := s.M.Store().Environments(r.Context())
	if err != nil {
		replyErr(w, err)
		return
	}
	who := actor(r)
	out := []store.Environment{}
	for _, e := range list {
		if access.UseEnvironment(who, e) {
			out = append(out, e)
		}
	}
	reply(w, http.StatusOK, out)
}

// environment loads the project a request names, if the viewer may use it,
// and checks they may manage it when manage is set; otherwise it replies.
func (s *Server) environment(w http.ResponseWriter, r *http.Request, id string, manage bool) (store.Environment, bool) {
	e, err := s.M.Store().Environment(r.Context(), id)
	if err != nil {
		replyErr(w, err)
		return e, false
	}
	who := actor(r)
	if !access.UseEnvironment(who, e) {
		hidden(w)
		return e, false
	}
	if manage && !access.ManageEnvironment(who, e) {
		forbid(w, "change this project")
		return e, false
	}
	return e, true
}

func (s *Server) getEnvironment(w http.ResponseWriter, r *http.Request) {
	if e, ok := s.environment(w, r, r.PathValue("id"), false); ok {
		reply(w, http.StatusOK, e)
	}
}

func (s *Server) createEnvironment(w http.ResponseWriter, r *http.Request) {
	var in environmentReq
	if !decode(w, r, &in) {
		return
	}
	team, ok := s.target(w, r, in.TeamID, "")
	if !ok {
		return
	}
	e := store.Environment{ID: store.NewID(), TeamID: team}
	if err := in.apply(&e); err != nil {
		replyErr(w, err)
		return
	}
	if err := s.M.Store().CreateEnvironment(r.Context(), &e); err != nil {
		replyErr(w, named(err, e.Name))
		return
	}
	reply(w, http.StatusCreated, e)
}

func (s *Server) updateEnvironment(w http.ResponseWriter, r *http.Request) {
	var in environmentReq
	if !decode(w, r, &in) {
		return
	}
	e, ok := s.environment(w, r, r.PathValue("id"), true)
	if !ok {
		return
	}
	if in.TeamID != "" && in.TeamID != e.TeamID {
		fail(w, http.StatusBadRequest, "a project's team can't change")
		return
	}
	if err := in.apply(&e); err != nil {
		replyErr(w, err)
		return
	}
	if err := s.M.Store().SaveEnvironment(r.Context(), &e); err != nil {
		replyErr(w, named(err, e.Name))
		return
	}
	reply(w, http.StatusOK, e)
}

func (s *Server) deleteEnvironment(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.environment(w, r, r.PathValue("id"), true); !ok {
		return
	}
	if err := s.M.Store().DeleteEnvironment(r.Context(), r.PathValue("id")); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func named(err error, name string) error {
	if err == store.ErrConflict {
		return fmt.Errorf("%w: a project named %q", store.ErrConflict, name)
	}
	return err
}

// ------------------------------------------------------------------ secrets

var repoScope = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// checkScope accepts "" (global), owner/name, env:<existing id> or
// team:<existing id>, and checks the viewer may read it, or write it when
// write is set.
func (s *Server) checkScope(r *http.Request, scope string, write bool) error {
	ctx, who := r.Context(), actor(r)
	var env *store.Environment
	switch {
	case scope == "" || repoScope.MatchString(scope):
	case strings.HasPrefix(scope, "env:"):
		e, err := s.M.Store().Environment(ctx, strings.TrimPrefix(scope, "env:"))
		if err != nil || !access.UseEnvironment(who, e) {
			return fmt.Errorf("%w: no project %q", store.ErrInvalid, strings.TrimPrefix(scope, "env:"))
		}
		env = &e
	case store.IsTeamScope(scope):
		if _, err := s.M.Store().Team(ctx, store.TeamOfScope(scope)); err != nil {
			return fmt.Errorf("%w: no team %q", store.ErrInvalid, store.TeamOfScope(scope))
		}
	default:
		return fmt.Errorf("%w: scope must be empty (global), owner/name, env:<id> or team:<id>", store.ErrInvalid)
	}
	if !access.ReadScope(who, scope, env) || (write && !access.WriteScope(who, scope, env)) {
		return errForbidden
	}
	return nil
}

// errForbidden is a request the viewer may not make.
var errForbidden = errors.New("you can't change that: ask an admin or the team's lead")

// scopeErr replies to a failed checkScope.
func scopeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, errForbidden) {
		fail(w, http.StatusForbidden, err.Error())
		return
	}
	replyErr(w, err)
}

// listSecrets lists secret names the viewer may see: all, or with ?scope=
// only that scope.
func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	var scope *string
	if r.URL.Query().Has("scope") {
		scope = ptr(r.URL.Query().Get("scope"))
		if *scope == store.CredentialScope {
			fail(w, http.StatusBadRequest, "use /api/credentials")
			return
		}
		if err := s.checkScope(r, *scope, false); err != nil {
			scopeErr(w, err)
			return
		}
	}
	list, err := s.M.Store().Secrets(r.Context(), scope)
	if err != nil {
		replyErr(w, err)
		return
	}
	out := []store.SecretInfo{}
	for _, x := range list {
		if scope != nil || s.checkScope(r, x.Scope, false) == nil {
			out = append(out, x)
		}
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) putSecret(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Scope string `json:"scope"`
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.checkScope(r, in.Scope, true); err != nil {
		scopeErr(w, err)
		return
	}
	if err := s.M.Store().SetSecret(r.Context(), in.Scope, strings.TrimSpace(in.Name), in.Value); err != nil {
		replyErr(w, err)
		return
	}
	s.auditSecret(r, in.Scope, "secret.set", strings.TrimSpace(in.Name))
	w.WriteHeader(http.StatusNoContent)
}

// importSecrets stores every variable of a .env file in one scope.
func (s *Server) importSecrets(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Scope string `json:"scope"`
		Text  string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.checkScope(r, in.Scope, true); err != nil {
		scopeErr(w, err)
		return
	}
	vars, skipped := ParseDotenv(in.Text)
	imported := []string{}
	for _, v := range vars {
		if err := s.M.Store().SetSecret(r.Context(), in.Scope, v.Name, v.Value); err != nil {
			skipped = append(skipped, Skipped{Line: v.Line, Reason: strings.TrimPrefix(err.Error(), store.ErrInvalid.Error()+": ")})
			continue
		}
		imported = append(imported, v.Name)
		s.auditSecret(r, in.Scope, "secret.set", v.Name)
	}
	reply(w, http.StatusOK, map[string]any{"imported": imported, "skipped": orEmpty(skipped)})
}

// effectiveSecrets shows which variables a session would receive, and from
// which scope: ?environment=<id>, or ?repos=owner/a,owner/b (primary first).
func (s *Server) effectiveSecrets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	t := store.SecretTarget{InheritGlobal: true, TeamID: q.Get("team")}
	if t.TeamID != "" && !access.UseTeam(actor(r), t.TeamID) {
		hidden(w)
		return
	}
	if id := q.Get("environment"); id != "" {
		e, ok := s.environment(w, r, id, false)
		if !ok {
			return
		}
		t.EnvironmentID, t.InheritGlobal, t.TeamID = e.ID, e.InheritSecrets, firstOf(e.TeamID, t.TeamID)
	} else if v := q.Get("repos"); v != "" {
		t.Repos = strings.Split(v, ",")
	}
	list, err := s.M.Store().EffectiveSecrets(r.Context(), t)
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"layers": t.Layers(), "secrets": list})
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("scope") == store.CredentialScope {
		fail(w, http.StatusBadRequest, "use /api/credentials")
		return
	}
	if err := s.checkScope(r, q.Get("scope"), true); err != nil {
		scopeErr(w, err)
		return
	}
	if err := s.M.Store().DeleteSecret(r.Context(), q.Get("scope"), q.Get("name")); err != nil {
		replyErr(w, err)
		return
	}
	s.auditSecret(r, q.Get("scope"), "secret.deleted", q.Get("name"))
	w.WriteHeader(http.StatusNoContent)
}

// auditSecret records a change to a secret: its scope and name, never its
// value.
func (s *Server) auditSecret(r *http.Request, scope, action, name string) {
	team := ""
	if store.IsTeamScope(scope) {
		team = store.TeamOfScope(scope)
	}
	s.audit(r, team, action, name, map[string]string{"scope": scope})
}

// -------------------------------------------------------------- MCP servers

type mcpReq struct {
	Name    string             `json:"name"`
	Scope   string             `json:"scope"`
	Type    string             `json:"type"`
	URL     string             `json:"url"`
	Command []string           `json:"command"`
	Secret  *map[string]string `json:"secret"` // headers or env; omitted = unchanged; "" values keep existing ones
	Enabled *bool              `json:"enabled"`
}

func (s *Server) applyMCP(r *http.Request, in mcpReq, m *store.MCPServer) error {
	if err := s.checkScope(r, in.Scope, true); err != nil {
		return err
	}
	m.Name, m.Scope, m.Type, m.URL, m.Command = strings.TrimSpace(in.Name), in.Scope, in.Type, strings.TrimSpace(in.URL), in.Command
	if m.Type == "http" {
		m.Command = nil
	} else {
		m.URL = ""
	}
	if in.Enabled != nil {
		m.Enabled = *in.Enabled
	}
	if in.Secret != nil {
		next := map[string]string{}
		for k, v := range *in.Secret {
			if v == "" {
				if old, ok := m.Secret[k]; ok {
					next[k] = old // write-only values: blank keeps the stored one
				}
				continue
			}
			next[k] = v
		}
		m.Secret = next
	}
	return session.ValidateMCPServer(m)
}

// listMCP lists the MCP servers in scopes the viewer may see.
func (s *Server) listMCP(w http.ResponseWriter, r *http.Request) {
	list, err := s.M.Store().MCPServers(r.Context())
	if err != nil {
		replyErr(w, err)
		return
	}
	out := []store.MCPServer{}
	for _, m := range list {
		if s.checkScope(r, m.Scope, false) == nil {
			out = append(out, m)
		}
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) createMCP(w http.ResponseWriter, r *http.Request) {
	var in mcpReq
	if !decode(w, r, &in) {
		return
	}
	m := store.MCPServer{ID: store.NewID(), Enabled: true}
	if err := s.applyMCP(r, in, &m); err != nil {
		scopeErr(w, err)
		return
	}
	if err := s.M.Store().CreateMCPServer(r.Context(), &m); err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusCreated, m)
}

func (s *Server) updateMCP(w http.ResponseWriter, r *http.Request) {
	var in mcpReq
	if !decode(w, r, &in) {
		return
	}
	m, err := s.M.Store().MCPServer(r.Context(), r.PathValue("id"))
	if err != nil {
		replyErr(w, err)
		return
	}
	if err := s.checkScope(r, m.Scope, true); err != nil {
		scopeErr(w, err)
		return
	}
	if err := s.applyMCP(r, in, &m); err != nil {
		scopeErr(w, err)
		return
	}
	if err := s.M.Store().SaveMCPServer(r.Context(), &m); err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, m)
}

func (s *Server) deleteMCP(w http.ResponseWriter, r *http.Request) {
	m, err := s.M.Store().MCPServer(r.Context(), r.PathValue("id"))
	if err != nil {
		replyErr(w, err)
		return
	}
	if err := s.checkScope(r, m.Scope, true); err != nil {
		scopeErr(w, err)
		return
	}
	if err := s.M.Store().DeleteMCPServer(r.Context(), r.PathValue("id")); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------- repositories

// listRepos lists GitHub repositories the server can work on, for pickers.
func (s *Server) listRepos(w http.ResponseWriter, r *http.Request) {
	if s.GitHub.Mode(r.Context()) == "" {
		reply(w, http.StatusOK, []any{})
		return
	}
	repos, err := s.GitHub.Repos(r.Context())
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	reply(w, http.StatusOK, orEmpty(repos))
}

func (s *Server) listBranches(w http.ResponseWriter, r *http.Request) {
	repo := r.PathValue("owner") + "/" + r.PathValue("name")
	if !repoScope.MatchString(repo) {
		fail(w, http.StatusBadRequest, "invalid repository")
		return
	}
	branches, err := s.GitHub.Branches(r.Context(), repo)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	reply(w, http.StatusOK, orEmpty(branches))
}

func ptr[T any](v T) *T { return &v }
