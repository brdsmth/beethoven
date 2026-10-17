package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/brdsmth/beethoven/internal/store"
)

// Owner is an account a new repository can be created in.
type Owner struct {
	Login string `json:"login"`
	Type  string `json:"type"` // User or Organization
	// Why a repository can't be created there, if it can't, and where to
	// fix that.
	Problem string `json:"problem,omitempty"`
	FixURL  string `json:"fix_url,omitempty"`
}

// mayCreate reports whether an app's permissions let it create repositories.
func mayCreate(p map[string]string) bool {
	return p["repository_creation"] == "write" || p["administration"] == "write"
}

// Owners lists the accounts the person can create repositories in, theirs
// first. With a connected app, those are the accounts it is installed on,
// each saying whether the app may create repositories there; with a
// personal access token, the person and their organizations.
func (c *Client) Owners(ctx context.Context) ([]Owner, error) {
	cn, err := c.connection(ctx)
	if err != nil {
		return nil, err
	}
	tok, err := c.bearer(ctx)
	if err != nil {
		return nil, err
	}
	if cn == nil {
		login, err := c.User(ctx)
		if err != nil {
			return nil, err
		}
		var orgs []struct {
			Login string `json:"login"`
		}
		if err := c.call(ctx, tok, "GET", "/user/orgs?per_page=100", nil, &orgs); err != nil {
			return nil, err
		}
		out := []Owner{{Login: login, Type: "User"}}
		for _, o := range orgs {
			out = append(out, Owner{Login: o.Login, Type: "Organization"})
		}
		return out, nil
	}
	insts, err := c.installations(ctx, cn)
	if err != nil {
		return nil, err
	}
	var app *appInfo // looked up once some installation can't create repositories
	out := []Owner{}
	for _, in := range insts {
		if in.Type == "User" && !strings.EqualFold(in.Account, cn.acct.Login) {
			continue
		}
		o := Owner{Login: in.Account, Type: in.Type}
		if !mayCreate(in.permissions) {
			if app == nil {
				app = c.appInfo(ctx, cn.app)
			}
			o.Problem, o.FixURL = c.problem(app, cn.app, in)
		}
		out = append(out, o)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Type == "User" && out[j].Type != "User" })
	return out, nil
}

// appInfo is what GitHub says about the app: what it asks of the accounts
// that install it, and who owns it.
type appInfo struct {
	Permissions map[string]string `json:"permissions"`
	Owner       struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"owner"`
}

// appInfo looks the app up, as far as GitHub answers.
func (c *Client) appInfo(ctx context.Context, app *App) *appInfo {
	info := &appInfo{}
	if jwt, err := app.jwt(); err == nil {
		c.call(ctx, jwt, "GET", "/app", nil, info)
	}
	if info.Owner.Login == "" {
		info.Owner.Login = app.Owner.Login
	}
	return info
}

// problem explains why the app can't create repositories in the account of
// an installation, and links to where that is fixed.
func (c *Client) problem(a *appInfo, app *App, in Installation) (string, string) {
	if mayCreate(a.Permissions) {
		return fmt.Sprintf("The GitHub App now asks to create repositories, but %s hasn't accepted yet. "+
			"Review the request on the app's installation, then check again.", in.Account), in.URL
	}
	settings := "/settings/apps/" + app.Slug + "/permissions"
	if a.Owner.Type == "Organization" {
		settings = "/organizations/" + url.PathEscape(a.Owner.Login) + settings
	}
	return fmt.Sprintf("The GitHub App can't create repositories yet. In its settings (it belongs to @%s), "+
		"set the repository permission Repository creation to Read and write, and save. Then accept the new "+
		"permission on the app's installation in %s, and check again.", a.Owner.Login, in.Account), c.web() + settings
}

// NewRepo describes a repository to create.
type NewRepo struct {
	Owner       string // an account from Owners
	Name        string
	Description string
	Private     bool
	Gitignore   string // one of GitHub's .gitignore templates, like Go; "" for none
}

// CreateRepo creates a repository as the person, with a first commit on
// its default branch: a README and, with a template, a .gitignore.
func (c *Client) CreateRepo(ctx context.Context, nr NewRepo) (Repo, error) {
	tok, err := c.bearer(ctx)
	if err != nil {
		return Repo{}, err
	}
	login, err := c.User(ctx)
	if err != nil {
		return Repo{}, err
	}
	path := "/user/repos"
	if !strings.EqualFold(nr.Owner, login) {
		path = "/orgs/" + url.PathEscape(nr.Owner) + "/repos"
	}
	body := map[string]any{"name": nr.Name, "description": nr.Description, "private": nr.Private, "auto_init": true}
	if nr.Gitignore != "" {
		body["gitignore_template"] = nr.Gitignore
	}
	var out repoJSON
	err = c.call(ctx, tok, "POST", path, body, &out)
	var e *apiError
	switch {
	case errors.As(err, &e) && e.Status == http.StatusUnprocessableEntity && strings.Contains(e.msg, "already exists"):
		return Repo{}, fmt.Errorf("%w: %s/%s already exists on GitHub; choose another name", store.ErrInvalid, nr.Owner, nr.Name)
	case errors.As(err, &e) && (e.Status == http.StatusForbidden || e.Status == http.StatusNotFound):
		return Repo{}, fmt.Errorf("GitHub didn't let Beethoven create a repository in %s (%s)", nr.Owner, e.msg)
	case err != nil:
		return Repo{}, err
	}
	c.mu.Lock()
	c.repos = nil // list it straight away
	c.mu.Unlock()
	return out.repo(), nil
}
