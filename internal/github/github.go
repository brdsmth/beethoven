// Package github implements session.SCM against the GitHub REST API.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/brdsmth/beethoven/internal/session"
)

type Client struct {
	API      string // default https://api.github.com
	CloneFmt string // default https://github.com/%s.git
	HTTP     *http.Client
	// Stored returns a token configured in the server's settings, which
	// takes precedence over the one given to New.
	Stored func(context.Context) string

	mu       sync.Mutex
	token    string
	gh       bool // fall back to `gh auth token`
	fromGH   bool // token came from gh
	repos    []Repo
	reposFor string // the token the cached repos were listed with
	reposAt  time.Time
}

// New uses token if set, otherwise asks the gh CLI on demand.
func New(token string) *Client {
	return &Client{token: token, gh: token == ""}
}

// Source says where the token comes from: "settings", "environment",
// "gh" or "" when there is none.
func (c *Client) Source(ctx context.Context) string {
	if c.Stored != nil && c.Stored(ctx) != "" {
		return "settings"
	}
	tok, _ := c.Token(ctx)
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

func (c *Client) CloneURL(repo string) string {
	f := c.CloneFmt
	if f == "" {
		f = "https://github.com/%s.git"
	}
	return fmt.Sprintf(f, repo)
}

func (c *Client) Token(ctx context.Context) (string, error) {
	if c.Stored != nil {
		if tok := c.Stored(ctx); tok != "" {
			return tok, nil
		}
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
	return c.token, nil
}

// User returns the login the token belongs to.
func (c *Client) User(ctx context.Context) (string, error) {
	var u struct {
		Login string `json:"login"`
	}
	err := c.do(ctx, "GET", "/user", nil, &u)
	return u.Login, err
}

type Repo struct {
	Repo          string `json:"repo"` // owner/name
	Description   string `json:"description,omitempty"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
}

// Repos lists repositories the token can push to, most recently pushed
// first. The list is cached for five minutes.
func (c *Client) Repos(ctx context.Context) ([]Repo, error) {
	tok, _ := c.Token(ctx)
	c.mu.Lock()
	if c.reposFor == tok && time.Since(c.reposAt) < 5*time.Minute {
		defer c.mu.Unlock()
		return c.repos, nil
	}
	c.mu.Unlock()
	var out []Repo
	for page := 1; page <= 5; page++ {
		var batch []struct {
			FullName      string `json:"full_name"`
			Description   string `json:"description"`
			Private       bool   `json:"private"`
			Archived      bool   `json:"archived"`
			DefaultBranch string `json:"default_branch"`
			Permissions   struct {
				Push bool `json:"push"`
			} `json:"permissions"`
		}
		q := url.Values{"per_page": {"100"}, "page": {fmt.Sprint(page)}, "sort": {"pushed"},
			"affiliation": {"owner,collaborator,organization_member"}}
		if err := c.do(ctx, "GET", "/user/repos?"+q.Encode(), nil, &batch); err != nil {
			return nil, err
		}
		for _, r := range batch {
			if !r.Archived && r.Permissions.Push {
				out = append(out, Repo{Repo: r.FullName, Description: r.Description, Private: r.Private, DefaultBranch: r.DefaultBranch})
			}
		}
		if len(batch) < 100 {
			break
		}
	}
	c.mu.Lock()
	c.repos, c.reposFor, c.reposAt = out, tok, time.Now()
	c.mu.Unlock()
	return out, nil
}

// Branches lists a repository's branches.
func (c *Client) Branches(ctx context.Context, repo string) ([]string, error) {
	var out []string
	for page := 1; page <= 3; page++ {
		var batch []struct {
			Name string `json:"name"`
		}
		if err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/branches?per_page=100&page=%d", repo, page), nil, &batch); err != nil {
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
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.do(ctx, "GET", "/repos/"+repo, nil, &r); err != nil {
		return "", err
	}
	return r.DefaultBranch, nil
}

// OpenPR opens a pull request, or returns the open one for the same head.
func (c *Client) OpenPR(ctx context.Context, pr session.PullRequest) (string, error) {
	owner, _, _ := strings.Cut(pr.Repo, "/")
	var existing []struct {
		HTMLURL string `json:"html_url"`
	}
	q := url.Values{"head": {owner + ":" + pr.Head}, "state": {"open"}}
	if err := c.do(ctx, "GET", "/repos/"+pr.Repo+"/pulls?"+q.Encode(), nil, &existing); err != nil {
		return "", err
	}
	if len(existing) > 0 {
		return existing[0].HTMLURL, nil
	}
	var created struct {
		HTMLURL string `json:"html_url"`
	}
	err := c.do(ctx, "POST", "/repos/"+pr.Repo+"/pulls", map[string]any{
		"title": pr.Title, "head": pr.Head, "base": pr.Base, "body": pr.Body, "draft": pr.Draft,
	}, &created)
	return created.HTMLURL, err
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	api := c.API
	if api == "" {
		api = "https://api.github.com"
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, api+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if tok, _ := c.Token(ctx); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
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
		return fmt.Errorf("github %s %s: %s: %s", method, strings.SplitN(path, "?", 2)[0], resp.Status, msg)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}
