package api

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/brdsmth/beethoven/internal/github"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

// Starting a project creates what sessions need behind the scenes: a
// GitHub repository with a first commit, and an environment working on it
// that installs the project's toolchain and holds its variables.

// A stack is what a new project is built with: GitHub's .gitignore
// template for it, and the capability (see internal/capability) that
// installs its toolchain, its dependencies once it has any, and teaches the
// agent how to work with it. The project switches the capability on, so
// it's one setting to change later. Node needs no capability, as the
// sandbox has it: a setup script installs the project's packages.
type stack struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Hint       string `json:"hint"`
	Gitignore  string `json:"-"`
	Capability string `json:"capability,omitempty"`
	Setup      string `json:"setup,omitempty"`
}

var stacks = []stack{
	{ID: "", Name: "Anything", Hint: "Node 24 and Python 3 come with the sandbox; the agent adds the capabilities it needs."},
	{ID: "node", Name: "Node.js", Gitignore: "Node", Hint: "Node 24 and npm; installs the project's packages.",
		Setup: `# Node 24 and npm come with the sandbox. Install the project's packages once it has any.
if [ -f package-lock.json ]; then npm ci; elif [ -f package.json ]; then npm install; fi`},
	{ID: "expo", Name: "Expo (React Native)", Gitignore: "Node", Capability: "expo",
		Hint: "A mobile app: Expo's tools, with builds and phone previews through EAS. Add EXPO_TOKEN below for those."},
	{ID: "bun", Name: "Bun", Gitignore: "Node", Capability: "bun", Hint: "Installs Bun, then the project's packages."},
	{ID: "python", Name: "Python", Gitignore: "Python", Capability: "python", Hint: "Python 3 with uv; installs the project's dependencies."},
	{ID: "go", Name: "Go", Gitignore: "Go", Capability: "go", Hint: "Installs the latest Go, then the project's modules."},
	{ID: "rust", Name: "Rust", Gitignore: "Rust", Capability: "rust", Hint: "Installs stable Rust with Cargo, then the project's crates."},
}

func stackByID(id string) (stack, bool) {
	for _, s := range stacks {
		if s.ID == id {
			return s, true
		}
	}
	return stack{}, false
}

// projectOptions says where a new project's repository can go, and what it
// can be built with.
func (s *Server) projectOptions(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"owners": []github.Owner{}, "stacks": stacks}
	if s.GitHub.Mode(r.Context()) != "" {
		if owners, err := s.GitHub.Owners(r.Context()); err != nil {
			out["error"] = err.Error()
		} else {
			out["owners"] = owners
		}
	}
	reply(w, http.StatusOK, out)
}

type projectReq struct {
	Owner       string `json:"owner"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Public      bool   `json:"public"`
	Stack       string `json:"stack"`
	Variables   string `json:"variables"` // a .env file: the environment's secrets
	Prompt      string `json:"prompt"`    // starts a first session, as do attached files
	TeamID      string `json:"team_id"`   // the team the project and its sessions belong to; "" = the workspace
	Model       string `json:"model"`
	Effort      string `json:"effort"`
}

var repoName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

// createProject starts a project: a GitHub repository with a first commit,
// an environment for it with the stack's setup script and the variables as
// its secrets and, given a prompt or files, a first session there.
// Everything is checked before the repository is created.
func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var in projectReq
	files, ok := decodeWithFiles(w, r, &in)
	if !ok {
		return
	}
	ctx := r.Context()
	in.Owner, in.Name, in.Description = strings.TrimSpace(in.Owner), strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	st, known := stackByID(in.Stack)
	vars, skipped := ParseDotenv(in.Variables)
	switch {
	case s.GitHub.Mode(ctx) == "":
		fail(w, http.StatusConflict, "connect GitHub first")
		return
	case !ownerName.MatchString(in.Owner):
		fail(w, http.StatusBadRequest, "choose the account the repository belongs to")
		return
	case !repoName.MatchString(in.Name) || strings.Trim(in.Name, ".") == "" || strings.HasSuffix(strings.ToLower(in.Name), ".git"):
		fail(w, http.StatusBadRequest, "a repository name uses letters, digits, '.', '-' and '_'")
		return
	case len(in.Description) > 350:
		fail(w, http.StatusBadRequest, "the description is longer than GitHub's 350 characters")
		return
	case !known:
		fail(w, http.StatusBadRequest, fmt.Sprintf("unknown stack %q", in.Stack))
		return
	case len(skipped) > 0:
		fail(w, http.StatusBadRequest, fmt.Sprintf("variables, line %d: %s", skipped[0].Line, skipped[0].Reason))
		return
	case len(vars) > store.MaxSecretsPerScope:
		fail(w, http.StatusBadRequest, fmt.Sprintf("at most %d variables", store.MaxSecretsPerScope))
		return
	}
	err := session.ValidateOverrides(store.Overrides{Model: in.Model, Effort: in.Effort})
	if err != nil {
		replyErr(w, err)
		return
	}
	team, ok := s.target(w, r, in.TeamID, "")
	if !ok {
		return
	}
	env := store.Environment{ID: store.NewID(), TeamID: team, Description: in.Description, SetupScript: st.Setup,
		InheritSecrets: true}
	if st.Capability != "" {
		env.Overrides.Capabilities = []string{st.Capability}
	}
	env.Name, err = s.environmentName(r, team, in.Name, in.Owner+"/"+in.Name)
	if err != nil {
		replyErr(w, err)
		return
	}

	repo, err := s.GitHub.CreateRepo(ctx, github.NewRepo{Owner: in.Owner, Name: in.Name, Description: in.Description,
		Private: !in.Public, Gitignore: st.Gitignore})
	switch {
	case errors.Is(err, store.ErrInvalid):
		replyErr(w, err)
		return
	case err != nil:
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	// From here on the repository exists: a failure says so.
	partly := func(what string, err error) {
		fail(w, http.StatusInternalServerError, fmt.Sprintf("created %s on GitHub, but %s: %v", repo.Repo, what, err))
	}
	env.Repos = []store.RepoRef{{Repo: repo.Repo}} // its default branch
	if err := session.ValidateEnvironment(&env); err != nil {
		partly("not its environment", err)
		return
	}
	if err := s.M.Store().CreateEnvironment(ctx, &env); err != nil {
		partly("not its environment", err)
		return
	}
	for _, v := range vars {
		if err := s.M.Store().SetSecret(ctx, store.EnvScope(env.ID), v.Name, v.Value); err != nil {
			partly("couldn't store "+v.Name, err)
			return
		}
	}
	out := map[string]any{"repo": repo, "environment": env}
	if strings.TrimSpace(in.Prompt) != "" || len(files) > 0 {
		sess, p, err := s.M.Submit(ctx, session.Request{Prompt: in.Prompt, Attachments: files, EnvironmentID: env.ID,
			Model: in.Model, Effort: in.Effort, Author: s.author(r), AccountID: accountOf(r), TeamID: team, Source: "web"})
		if err != nil {
			partly("couldn't start the session", err)
			return
		}
		out["session"], out["prompt"] = sess, p
	}
	reply(w, http.StatusCreated, out)
}

// environmentName is the first of names that none of a team's projects
// (or, for "", the workspace's) has.
func (s *Server) environmentName(r *http.Request, team string, names ...string) (string, error) {
	list, err := s.M.Store().Environments(r.Context())
	if err != nil {
		return "", err
	}
next:
	for _, name := range names {
		for _, e := range list {
			if e.TeamID == team && strings.EqualFold(e.Name, name) {
				continue next
			}
		}
		return name, nil
	}
	return "", fmt.Errorf("%w: a project named %s", store.ErrConflict, names[0])
}
