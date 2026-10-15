package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

// author is the commit author for prompts sent through the API.
func (s *Server) author(r *http.Request) store.Author {
	eff, err := s.M.Effective(r.Context(), "")
	if err != nil {
		return store.Author{}
	}
	return eff.Author
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

func (s *Server) listEnvironments(w http.ResponseWriter, r *http.Request) {
	list, err := s.M.Store().Environments(r.Context())
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, orEmpty(list))
}

func (s *Server) getEnvironment(w http.ResponseWriter, r *http.Request) {
	e, err := s.M.Store().Environment(r.Context(), r.PathValue("id"))
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, e)
}

func (s *Server) createEnvironment(w http.ResponseWriter, r *http.Request) {
	var in environmentReq
	if !decode(w, r, &in) {
		return
	}
	e := store.Environment{ID: store.NewID()}
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
	e, err := s.M.Store().Environment(r.Context(), r.PathValue("id"))
	if err != nil {
		replyErr(w, err)
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
	if err := s.M.Store().DeleteEnvironment(r.Context(), r.PathValue("id")); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func named(err error, name string) error {
	if err == store.ErrConflict {
		return fmt.Errorf("%w: an environment named %q", store.ErrConflict, name)
	}
	return err
}

// ------------------------------------------------------------------ secrets

var repoScope = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// checkScope accepts "" (global), owner/name, or env:<existing id>.
func (s *Server) checkScope(ctx context.Context, scope string) error {
	switch {
	case scope == "" || repoScope.MatchString(scope):
		return nil
	case strings.HasPrefix(scope, "env:"):
		if _, err := s.M.Store().Environment(ctx, strings.TrimPrefix(scope, "env:")); err != nil {
			return fmt.Errorf("%w: no environment %q", store.ErrInvalid, strings.TrimPrefix(scope, "env:"))
		}
		return nil
	}
	return fmt.Errorf("%w: scope must be empty (global), owner/name, or env:<id>", store.ErrInvalid)
}

// listSecrets lists secret names: all, or with ?scope= only that scope.
func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	var scope *string
	if r.URL.Query().Has("scope") {
		scope = ptr(r.URL.Query().Get("scope"))
		if *scope == store.CredentialScope {
			fail(w, http.StatusBadRequest, "use /api/credentials")
			return
		}
	}
	list, err := s.M.Store().Secrets(r.Context(), scope)
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, orEmpty(list))
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
	if err := s.checkScope(r.Context(), in.Scope); err != nil {
		replyErr(w, err)
		return
	}
	if err := s.M.Store().SetSecret(r.Context(), in.Scope, strings.TrimSpace(in.Name), in.Value); err != nil {
		replyErr(w, err)
		return
	}
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
	if err := s.checkScope(r.Context(), in.Scope); err != nil {
		replyErr(w, err)
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
	}
	reply(w, http.StatusOK, map[string]any{"imported": imported, "skipped": orEmpty(skipped)})
}

// effectiveSecrets shows which variables a session would receive, and from
// which scope: ?environment=<id>, or ?repos=owner/a,owner/b (primary first).
func (s *Server) effectiveSecrets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	t := store.SecretTarget{InheritGlobal: true}
	if id := q.Get("environment"); id != "" {
		e, err := s.M.Store().Environment(r.Context(), id)
		if err != nil {
			replyErr(w, err)
			return
		}
		t.EnvironmentID, t.InheritGlobal = e.ID, e.InheritSecrets
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
	if err := s.M.Store().DeleteSecret(r.Context(), q.Get("scope"), q.Get("name")); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func (s *Server) applyMCP(ctx context.Context, in mcpReq, m *store.MCPServer) error {
	if err := s.checkScope(ctx, in.Scope); err != nil {
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

func (s *Server) listMCP(w http.ResponseWriter, r *http.Request) {
	list, err := s.M.Store().MCPServers(r.Context())
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, orEmpty(list))
}

func (s *Server) createMCP(w http.ResponseWriter, r *http.Request) {
	var in mcpReq
	if !decode(w, r, &in) {
		return
	}
	m := store.MCPServer{ID: store.NewID(), Enabled: true}
	if err := s.applyMCP(r.Context(), in, &m); err != nil {
		replyErr(w, err)
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
	if err := s.applyMCP(r.Context(), in, &m); err != nil {
		replyErr(w, err)
		return
	}
	if err := s.M.Store().SaveMCPServer(r.Context(), &m); err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, m)
}

func (s *Server) deleteMCP(w http.ResponseWriter, r *http.Request) {
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
