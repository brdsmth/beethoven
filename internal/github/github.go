// Package github implements session.SCM against the GitHub REST API.
//
// The server acts on GitHub as a GitHub App: a preconfigured one, shared by
// every server an operator runs, or one registered from the server itself
// with GitHub's app manifest flow. Each person connects their own GitHub by
// authorizing the app. Work done for a person (named by access.With in its
// context) then clones and pushes with one-hour installation tokens, each
// for a single repository the person can reach, and calls the API, opening
// pull requests included, as that person. Work done for nobody in
// particular acts as the server's owner. Until anyone connects, a personal
// access token (stored in the settings, GITHUB_TOKEN, or the gh CLI's) is
// used for everything.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brdsmth/beethoven/internal/access"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

type Client struct {
	API      string // default https://api.github.com
	Web      string // default https://github.com
	CloneFmt string // default https://github.com/%s.git
	HTTP     *http.Client
	DB       *store.Store // the app registered here, connected accounts and a stored token
	EnvApp   *App         // a preconfigured app; takes precedence over one registered here

	MergeableWait time.Duration // before asking again for a pull request's mergeability; default 2s

	mu        sync.Mutex
	token     string // a personal access token given to New
	gh        bool   // fall back to `gh auth token`
	fromGH    bool   // token came from gh
	parsed    *App   // the app registered here, parsed from parsedRaw
	parsedRaw string
	minted    map[string]minted
	repos     map[string]cachedRepos // by the token they were listed with

	refresh sync.Mutex // serializes token refreshes: a refresh token works once
}

// New uses token, if set, as the personal access token; otherwise it asks
// the gh CLI on demand.
func New(token string) *Client {
	return &Client{token: token, gh: token == ""}
}

type cachedRepos struct {
	list []Repo
	at   time.Time
}

// conn is a connected app: the app and the account it acts for.
type conn struct {
	app  *App
	acct store.GitHubAccount
}

// ErrNotConnected means the person work is done for hasn't connected their
// GitHub account.
var ErrNotConnected = errors.New("connect your GitHub account first, in Settings › GitHub")

// person is whom ctx acts for: the person access.With named, or else the
// server's first owner. named reports which.
func (c *Client) person(ctx context.Context) (id string, named bool) {
	if a, ok := access.From(ctx); ok && a.AccountID != "" {
		return a.AccountID, true
	}
	if c.DB != nil {
		if o, err := c.DB.FirstOwner(ctx); err == nil {
			return o.ID, false
		}
	}
	return "", false
}

// connection returns the app and the GitHub account of the person ctx acts
// for, or nil if they haven't connected one. Connections made before there
// were people count as the owner's; work for nobody in particular falls
// back on the most recent connection.
func (c *Client) connection(ctx context.Context) (*conn, error) {
	app, _ := c.App(ctx)
	if app == nil || c.DB == nil {
		return nil, nil
	}
	id, named := c.person(ctx)
	acct, err := c.DB.GitHubAccountOf(ctx, app.ID, id)
	if errors.Is(err, store.ErrNotFound) && id != "" && c.firstOwner(ctx, id) {
		acct, err = c.DB.GitHubAccountOf(ctx, app.ID, "")
	}
	if errors.Is(err, store.ErrNotFound) && !named {
		acct, err = c.DB.GitHubAccount(ctx, app.ID)
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &conn{app: app, acct: acct}, nil
}

func (c *Client) firstOwner(ctx context.Context, id string) bool {
	o, err := c.DB.FirstOwner(ctx)
	return err == nil && o.ID == id
}

// tokenAllowed reports whether the personal access token may stand in: only
// while nobody has connected the app, since it is the operator's own.
func (c *Client) tokenAllowed(ctx context.Context) bool {
	app, _ := c.App(ctx)
	if app == nil || c.DB == nil {
		return true
	}
	_, err := c.DB.GitHubAccount(ctx, app.ID)
	return errors.Is(err, store.ErrNotFound)
}

// Mode says how the server reaches GitHub for the person ctx acts for:
// "app" through their connected GitHub App account, "token" with a personal
// access token, or "" not at all.
func (c *Client) Mode(ctx context.Context) string {
	if cn, _ := c.connection(ctx); cn != nil {
		return "app"
	}
	if c.tokenAllowed(ctx) && c.personalToken(ctx) != "" {
		return "token"
	}
	return ""
}

// bearer is the token for API calls made for the person: theirs when they
// connected the app, otherwise, while nobody has, the personal access token.
func (c *Client) bearer(ctx context.Context) (string, error) {
	cn, err := c.connection(ctx)
	if err != nil {
		return "", err
	}
	if cn != nil {
		return c.userToken(ctx, cn)
	}
	if !c.tokenAllowed(ctx) {
		return "", ErrNotConnected
	}
	return c.personalToken(ctx), nil
}

// TokenSource says where the personal access token comes from: "settings",
// "environment", "gh" or "" when there is none.
func (c *Client) TokenSource(ctx context.Context) string {
	if c.storedToken(ctx) != "" {
		return "settings"
	}
	tok := c.personalToken(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case tok == "":
		return ""
	case c.fromGH:
		return "gh"
	}
	return "environment"
}

func (c *Client) storedToken(ctx context.Context) string {
	if c.DB == nil {
		return ""
	}
	creds, _ := c.DB.Credentials(ctx)
	return creds["GITHUB_TOKEN"]
}

func (c *Client) personalToken(ctx context.Context) string {
	if tok := c.storedToken(ctx); tok != "" {
		return tok
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == "" && c.gh {
		out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
		if err == nil {
			c.token, c.fromGH = strings.TrimSpace(string(out)), true
		}
		c.gh = false // ask once
	}
	return c.token
}

func (c *Client) CloneURL(repo string) string {
	f := c.CloneFmt
	if f == "" {
		f = "https://github.com/%s.git"
	}
	return fmt.Sprintf(f, repo)
}

// Token returns a credential to clone and push repo: with a connected app,
// a one-hour token for that repository alone.
func (c *Client) Token(ctx context.Context, repo string) (string, error) {
	cn, err := c.connection(ctx)
	if err != nil {
		return "", err
	}
	if cn == nil {
		if !c.tokenAllowed(ctx) {
			return "", ErrNotConnected
		}
		return c.personalToken(ctx), nil
	}
	return c.repoToken(ctx, cn, repo)
}

// Author is the connected person's commit identity, with the private
// address GitHub attributes to their account.
func (c *Client) Author(ctx context.Context) store.Author {
	cn, err := c.connection(ctx)
	if err != nil || cn == nil {
		return store.Author{}
	}
	name := cn.acct.Name
	if name == "" {
		name = cn.acct.Login
	}
	return store.Author{Name: name, Email: fmt.Sprintf("%d+%s@users.noreply.github.com", cn.acct.ID, cn.acct.Login)}
}

// User returns the login the server acts as.
func (c *Client) User(ctx context.Context) (string, error) {
	tok, err := c.bearer(ctx)
	if err != nil {
		return "", err
	}
	var u struct {
		Login string `json:"login"`
	}
	err = c.call(ctx, tok, "GET", "/user", nil, &u)
	return u.Login, err
}

type Repo struct {
	Repo          string `json:"repo"` // owner/name
	Description   string `json:"description,omitempty"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	pushed        time.Time
}

type repoJSON struct {
	FullName      string    `json:"full_name"`
	Description   string    `json:"description"`
	Private       bool      `json:"private"`
	Archived      bool      `json:"archived"`
	DefaultBranch string    `json:"default_branch"`
	PushedAt      time.Time `json:"pushed_at"`
	Permissions   struct {
		Push bool `json:"push"`
	} `json:"permissions"`
}

func (r repoJSON) usable() bool { return !r.Archived && r.Permissions.Push }

func (r repoJSON) repo() Repo {
	return Repo{Repo: r.FullName, Description: r.Description, Private: r.Private, DefaultBranch: r.DefaultBranch, pushed: r.PushedAt}
}

// Repos lists repositories the server can work on, most recently pushed
// first: with a connected app, those of its installations the person can
// push to. The list is cached for five minutes.
func (c *Client) Repos(ctx context.Context) ([]Repo, error) {
	cn, err := c.connection(ctx)
	if err != nil {
		return nil, err
	}
	tok, err := c.bearer(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if hit, ok := c.repos[tok]; ok && time.Since(hit.at) < 5*time.Minute {
		defer c.mu.Unlock()
		return hit.list, nil
	}
	c.mu.Unlock()
	var out []Repo
	if cn != nil {
		out, err = c.installationRepos(ctx, cn, tok)
	} else {
		out, err = c.userRepos(ctx, tok)
	}
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].pushed.After(out[j].pushed) })
	c.mu.Lock()
	if c.repos == nil || len(c.repos) > 100 {
		c.repos = map[string]cachedRepos{}
	}
	c.repos[tok] = cachedRepos{list: out, at: time.Now()}
	c.mu.Unlock()
	return out, nil
}

func (c *Client) userRepos(ctx context.Context, tok string) ([]Repo, error) {
	var out []Repo
	for page := 1; page <= 5; page++ {
		var batch []repoJSON
		q := url.Values{"per_page": {"100"}, "page": {fmt.Sprint(page)}, "sort": {"pushed"},
			"affiliation": {"owner,collaborator,organization_member"}}
		if err := c.call(ctx, tok, "GET", "/user/repos?"+q.Encode(), nil, &batch); err != nil {
			return nil, err
		}
		for _, r := range batch {
			if r.usable() {
				out = append(out, r.repo())
			}
		}
		if len(batch) < 100 {
			break
		}
	}
	return out, nil
}

func (c *Client) installationRepos(ctx context.Context, cn *conn, tok string) ([]Repo, error) {
	insts, err := c.installations(ctx, cn)
	if err != nil {
		return nil, err
	}
	var out []Repo
	for _, in := range insts {
		for page := 1; page <= 5; page++ {
			var batch struct {
				Repositories []repoJSON `json:"repositories"`
			}
			path := fmt.Sprintf("/user/installations/%d/repositories?per_page=100&page=%d", in.ID, page)
			if err := c.call(ctx, tok, "GET", path, nil, &batch); err != nil {
				return nil, err
			}
			for _, r := range batch.Repositories {
				if r.usable() {
					out = append(out, r.repo())
				}
			}
			if len(batch.Repositories) < 100 {
				break
			}
		}
	}
	return out, nil
}

// Branches lists a repository's branches.
func (c *Client) Branches(ctx context.Context, repo string) ([]string, error) {
	tok, err := c.bearer(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for page := 1; page <= 3; page++ {
		var batch []struct {
			Name string `json:"name"`
		}
		if err := c.call(ctx, tok, "GET", fmt.Sprintf("/repos/%s/branches?per_page=100&page=%d", repo, page), nil, &batch); err != nil {
			return nil, err
		}
		for _, b := range batch {
			out = append(out, b.Name)
		}
		if len(batch) < 100 {
			break
		}
	}
	return out, nil
}

func (c *Client) DefaultBranch(ctx context.Context, repo string) (string, error) {
	tok, err := c.bearer(ctx)
	if err != nil {
		return "", err
	}
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.call(ctx, tok, "GET", "/repos/"+repo, nil, &r); err != nil {
		return "", err
	}
	return r.DefaultBranch, nil
}

// OpenPR opens a pull request as the person, or returns the open one for
// the same head.
func (c *Client) OpenPR(ctx context.Context, pr session.PullRequest) (string, error) {
	tok, err := c.bearer(ctx)
	if err != nil {
		return "", err
	}
	owner, _, _ := strings.Cut(pr.Repo, "/")
	var existing []struct {
		HTMLURL string `json:"html_url"`
	}
	q := url.Values{"head": {owner + ":" + pr.Head}, "state": {"open"}}
	if err := c.call(ctx, tok, "GET", "/repos/"+pr.Repo+"/pulls?"+q.Encode(), nil, &existing); err != nil {
		return "", err
	}
	if len(existing) > 0 {
		return existing[0].HTMLURL, nil
	}
	var created struct {
		HTMLURL string `json:"html_url"`
	}
	err = c.call(ctx, tok, "POST", "/repos/"+pr.Repo+"/pulls", map[string]any{
		"title": pr.Title, "head": pr.Head, "base": pr.Base, "body": pr.Body, "draft": pr.Draft,
	}, &created)
	return created.HTMLURL, err
}

// apiError is a response from the GitHub API outside 2xx.
type apiError struct {
	Status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func isNotFound(err error) bool {
	var e *apiError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// call makes an API request authorized with token, if any.
func (c *Client) call(ctx context.Context, token, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.api()+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
			Errors  []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		json.Unmarshal(data, &e)
		msg := e.Message
		for _, x := range e.Errors {
			msg += "; " + x.Message
		}
		return &apiError{Status: resp.StatusCode,
			msg: fmt.Sprintf("github %s %s: %s: %s", method, strings.SplitN(path, "?", 2)[0], resp.Status, msg)}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) api() string {
	if c.API == "" {
		return "https://api.github.com"
	}
	return c.API
}

func (c *Client) web() string {
	if c.Web == "" {
		return "https://github.com"
	}
	return c.Web
}

func (c *Client) http() *http.Client {
	if c.HTTP == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return c.HTTP
}
