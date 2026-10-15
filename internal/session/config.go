package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/brdsmth/beethoven/internal/agent"
	"github.com/brdsmth/beethoven/internal/store"
)

const MaxRepos = 10

// Efforts are the reasoning effort levels an agent accepts, lowest first.
var Efforts = []string{"low", "medium", "high", "xhigh", "max"}

var (
	repoPattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	branchPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	modelPattern  = regexp.MustCompile(`^[A-Za-z0-9._:/\[\]-]{0,100}$`)
	namePattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// ValidateRepos checks and normalizes a workspace's repositories: at most
// MaxRepos, well formed, and with distinct checkout directories.
func ValidateRepos(refs []store.RepoRef) ([]store.RepoRef, error) {
	if len(refs) > MaxRepos {
		return nil, fmt.Errorf("%w: at most %d repositories", ErrInvalid, MaxRepos)
	}
	out := make([]store.RepoRef, 0, len(refs))
	dirs := map[string]bool{}
	for _, r := range refs {
		r = store.RepoRef{Repo: strings.TrimSpace(r.Repo), BaseBranch: strings.TrimSpace(r.BaseBranch)}
		if !repoPattern.MatchString(r.Repo) || strings.Contains(r.Repo, "..") || r.Dir() == "." {
			return nil, fmt.Errorf("%w: %q is not a repository (owner/name)", ErrInvalid, r.Repo)
		}
		if r.BaseBranch != "" && (!branchPattern.MatchString(r.BaseBranch) || strings.Contains(r.BaseBranch, "..")) {
			return nil, fmt.Errorf("%w: %q is not a valid branch name", ErrInvalid, r.BaseBranch)
		}
		if dirs[strings.ToLower(r.Dir())] {
			return nil, fmt.Errorf("%w: two repositories named %q would share /workspace/%s", ErrInvalid, r.Dir(), r.Dir())
		}
		dirs[strings.ToLower(r.Dir())] = true
		out = append(out, r)
	}
	return out, nil
}

// ValidateOverrides checks settings an environment or the global settings set.
func ValidateOverrides(o store.Overrides) error {
	switch {
	case !modelPattern.MatchString(o.Model):
		return fmt.Errorf("%w: %q is not a model name", ErrInvalid, o.Model)
	case o.Effort != "" && !slices.Contains(Efforts, o.Effort):
		return fmt.Errorf("%w: effort must be one of %s", ErrInvalid, strings.Join(Efforts, ", "))
	case o.CPUs < 0 || o.CPUs > 64 || o.MemoryMB < 0 || (o.MemoryMB > 0 && o.MemoryMB < 256):
		return fmt.Errorf("%w: CPUs must be 0–64 and memory at least 256 MB", ErrInvalid)
	case strings.ContainsAny(o.Image, " \t\n"):
		return fmt.Errorf("%w: %q is not an image name", ErrInvalid, o.Image)
	}
	return nil
}

func ValidateSettings(s store.Settings) error {
	if err := ValidateOverrides(s.Overrides); err != nil {
		return err
	}
	switch {
	case s.IdleMinutes < 0 || s.TurnMinutes < 0:
		return fmt.Errorf("%w: timeouts cannot be negative", ErrInvalid)
	case s.BranchPrefix != "" && (!branchPattern.MatchString(s.BranchPrefix) || strings.Contains(s.BranchPrefix, "..")):
		return fmt.Errorf("%w: %q is not a valid branch prefix", ErrInvalid, s.BranchPrefix)
	}
	return nil
}

// ValidateEnvironment checks and normalizes an environment definition.
func ValidateEnvironment(e *store.Environment) error {
	e.Name, e.Description = strings.TrimSpace(e.Name), strings.TrimSpace(e.Description)
	if e.Name == "" || len(e.Name) > 100 {
		return fmt.Errorf("%w: an environment needs a name of at most 100 characters", ErrInvalid)
	}
	repos, err := ValidateRepos(e.Repos)
	if err != nil {
		return err
	}
	e.Repos = repos
	return ValidateOverrides(e.Overrides)
}

// ValidateMCPServer checks an MCP server definition.
func ValidateMCPServer(m *store.MCPServer) error {
	switch {
	case !namePattern.MatchString(m.Name) || m.Name == "beethoven":
		return fmt.Errorf("%w: server names use letters, digits, - and _, and \"beethoven\" is taken", ErrInvalid)
	case m.Type == "http" && !(strings.HasPrefix(m.URL, "https://") || strings.HasPrefix(m.URL, "http://")):
		return fmt.Errorf("%w: an HTTP server needs an http(s) URL", ErrInvalid)
	case m.Type == "stdio" && len(m.Command) == 0:
		return fmt.Errorf("%w: a stdio server needs a command", ErrInvalid)
	case m.Type != "http" && m.Type != "stdio":
		return fmt.Errorf("%w: type must be http or stdio", ErrInvalid)
	}
	for k := range m.Secret {
		if k == "" || strings.ContainsAny(k, "\r\n") {
			return fmt.Errorf("%w: invalid header or variable name %q", ErrInvalid, k)
		}
	}
	return nil
}

// Effective is what a session runs with: the server's defaults, overridden
// by the global settings, overridden by its environment.
type Effective struct {
	store.Overrides
	IdleTimeout  time.Duration
	TurnTimeout  time.Duration
	DraftPRs     bool
	BranchPrefix string
	Author       store.Author
	Environment  *store.Environment // nil without one, or if it was deleted
}

// Effective resolves settings for a session in environmentID ("" for none).
func (m *Manager) Effective(ctx context.Context, environmentID string) (Effective, error) {
	g, err := m.db.Settings(ctx)
	if err != nil {
		return Effective{}, err
	}
	e := Effective{
		Overrides:    g.Overrides.Over(store.Overrides{Image: m.cfg.Image}),
		IdleTimeout:  m.cfg.IdleTimeout,
		TurnTimeout:  m.cfg.TurnTimeout,
		DraftPRs:     g.DraftPRs,
		BranchPrefix: firstOf(g.BranchPrefix, "beethoven/"),
		Author:       m.DefaultAuthor(ctx),
	}
	if g.IdleMinutes > 0 {
		e.IdleTimeout = time.Duration(g.IdleMinutes) * time.Minute
	}
	if g.TurnMinutes > 0 {
		e.TurnTimeout = time.Duration(g.TurnMinutes) * time.Minute
	}
	if g.AuthorName != "" && g.AuthorEmail != "" {
		e.Author = store.Author{Name: g.AuthorName, Email: g.AuthorEmail}
	}
	if environmentID != "" {
		if env, err := m.db.Environment(ctx, environmentID); err == nil {
			e.Environment = &env
			e.Overrides = env.Overrides.Over(e.Overrides)
		}
	}
	return e, nil
}

// DefaultAuthor is the commit author when the settings name none: the
// person the git host acts for, else the server's own git identity.
func (m *Manager) DefaultAuthor(ctx context.Context) store.Author {
	if a := m.scm.Author(ctx); a.Name != "" && a.Email != "" {
		return a
	}
	return m.cfg.Author
}

// secretTarget decides which secret scopes reach a session's sandbox.
func secretTarget(s store.Session, e Effective) store.SecretTarget {
	inherit := true
	if e.Environment != nil {
		inherit = e.Environment.InheritSecrets
	}
	return store.SecretTarget{EnvironmentID: s.EnvironmentID, InheritGlobal: inherit, Repos: store.RepoNames(s.Repos)}
}

// mcpServers are the extra tool servers for a session's agent.
func (m *Manager) mcpServers(ctx context.Context, s store.Session) ([]agent.MCPServer, error) {
	list, err := m.db.MCPServersFor(ctx, s.EnvironmentID, store.RepoNames(s.Repos))
	if err != nil {
		return nil, err
	}
	out := make([]agent.MCPServer, 0, len(list))
	for _, x := range list {
		srv := agent.MCPServer{Name: x.Name, Type: x.Type, URL: x.URL, Command: x.Command}
		if x.Type == "http" {
			srv.Headers = x.Secret
		} else {
			srv.Env = x.Secret
		}
		out = append(out, srv)
	}
	return out, nil
}

// Workdir is where a session's agent works: the repository with one, the
// workspace holding them with several, or a scratch workspace with none.
func Workdir(s store.Session) string {
	if len(s.Repos) == 1 {
		return "/workspace/" + s.Repos[0].Dir()
	}
	return "/workspace"
}

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:6])
}
