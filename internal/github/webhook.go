package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/brdsmth/beethoven/internal/event"
)

// ErrBadSignature means a webhook delivery wasn't signed with the secret.
var ErrBadSignature = errors.New("github webhook: bad signature")

// WebhookEvents are the events the app subscribes to.
var WebhookEvents = []string{"issues", "issue_comment", "pull_request", "pull_request_review",
	"pull_request_review_comment", "check_suite", "workflow_run", "push"}

// WebhookSecret is the secret deliveries are signed with: the app's, else
// one set for repository webhooks.
func (c *Client) WebhookSecret(ctx context.Context) string {
	if app, _ := c.App(ctx); app != nil && app.WebhookSecret != "" {
		return app.WebhookSecret
	}
	return c.RepoWebhookSecret
}

// ParseWebhook verifies a delivery and translates it into an event. It
// returns nil for deliveries that aren't one: pings, events and actions no
// automation can trigger on.
func (c *Client) ParseWebhook(r *http.Request) (*event.Event, error) {
	secret := c.WebhookSecret(r.Context())
	if secret == "" {
		return nil, errors.New("github webhook: no secret is configured")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 25<<20))
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(r.Header.Get("X-Hub-Signature-256"))) {
		return nil, ErrBadSignature
	}
	e, err := Normalize(r.Header.Get("X-GitHub-Event"), r.Header.Get("X-GitHub-Delivery"), body)
	if err != nil || e == nil {
		return nil, err
	}
	if e.Kind == event.PRComment && e.Branch == "" {
		c.fillPullRequest(r.Context(), e) // comment payloads don't carry the branches
	}
	return e, nil
}

type user struct {
	Login string `json:"login"`
	Type  string `json:"type"` // User, Bot, Organization
}

type label struct {
	Name string `json:"name"`
}

type thread struct { // an issue or pull request
	Number  int     `json:"number"`
	Title   string  `json:"title"`
	Body    string  `json:"body"`
	HTMLURL string  `json:"html_url"`
	User    user    `json:"user"`
	Labels  []label `json:"labels"`
	Draft   bool    `json:"draft"`
	Merged  bool    `json:"merged"`
	Head    ref     `json:"head"`
	Base    ref     `json:"base"`
	// Set on an issue that is a pull request.
	PullRequest *struct{} `json:"pull_request"`
}

type ref struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}

type payload struct {
	Action     string `json:"action"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender      user    `json:"sender"`
	Issue       *thread `json:"issue"`
	PullRequest *thread `json:"pull_request"`
	Label       *label  `json:"label"`
	Comment     *struct {
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		Path    string `json:"path"`
		ID      int64  `json:"id"`
	} `json:"comment"`
	Review *struct {
		Body    string `json:"body"`
		State   string `json:"state"`
		HTMLURL string `json:"html_url"`
		ID      int64  `json:"id"`
	} `json:"review"`
	CheckSuite *struct {
		ID         int64  `json:"id"`
		HeadBranch string `json:"head_branch"`
		HeadSHA    string `json:"head_sha"`
		Conclusion string `json:"conclusion"`
		App        struct {
			Slug string `json:"slug"`
			Name string `json:"name"`
		} `json:"app"`
	} `json:"check_suite"`
	WorkflowRun *struct {
		ID         int64  `json:"id"`
		RunAttempt int    `json:"run_attempt"`
		Name       string `json:"name"`
		HeadBranch string `json:"head_branch"`
		HeadSHA    string `json:"head_sha"`
		Conclusion string `json:"conclusion"`
		HTMLURL    string `json:"html_url"`
	} `json:"workflow_run"`
	// push
	Ref     string `json:"ref"`
	After   string `json:"after"`
	Deleted bool   `json:"deleted"`
	Commits []any  `json:"commits"`
	Compare string `json:"compare"`
}

// Normalize translates one GitHub webhook payload. kind is the
// X-GitHub-Event header, delivery the X-GitHub-Delivery one.
func Normalize(kind, delivery string, body []byte) (*event.Event, error) {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("github webhook: %w", err)
	}
	e := &event.Event{Host: "github", Repo: p.Repository.FullName, Actor: p.Sender.Login, Bot: p.Sender.Type == "Bot", Delivery: delivery}
	if p.Label != nil {
		e.Label = p.Label.Name
	}
	switch kind {
	case "issues":
		e.Kind = map[string]string{"opened": event.IssueOpened, "labeled": event.IssueLabeled, "closed": event.IssueClosed}[p.Action]
		setThread(e, p.Issue, false)
	case "pull_request":
		switch p.Action {
		case "opened", "reopened":
			e.Kind = event.PROpened
		case "synchronize":
			e.Kind = event.PRUpdated
		case "ready_for_review":
			e.Kind = event.PRReady
		case "labeled":
			e.Kind = event.PRLabeled
		case "closed":
			e.Kind = event.PRClosed
			if p.PullRequest != nil && p.PullRequest.Merged {
				e.Kind = event.PRMerged
			}
		}
		setThread(e, p.PullRequest, true)
	case "issue_comment":
		if p.Action != "created" || p.Issue == nil || p.Comment == nil {
			return nil, nil
		}
		isPR := p.Issue.PullRequest != nil
		e.Kind = event.IssueComment
		if isPR {
			e.Kind = event.PRComment
		}
		setThread(e, p.Issue, isPR)
		e.Comment, e.CommentURL = p.Comment.Body, p.Comment.HTMLURL
	case "pull_request_review":
		if p.Action != "submitted" || p.Review == nil {
			return nil, nil
		}
		e.Kind = event.PRReview
		setThread(e, p.PullRequest, true)
		e.Comment, e.CommentURL, e.State = p.Review.Body, p.Review.HTMLURL, strings.ToLower(p.Review.State)
	case "pull_request_review_comment":
		if p.Action != "created" || p.Comment == nil {
			return nil, nil
		}
		e.Kind = event.ReviewComment
		setThread(e, p.PullRequest, true)
		e.Comment, e.CommentURL, e.Path = p.Comment.Body, p.Comment.HTMLURL, p.Comment.Path
	case "workflow_run": // GitHub Actions
		w := p.WorkflowRun
		if p.Action != "completed" || w == nil {
			return nil, nil
		}
		e.Kind, e.Branch, e.HeadSHA, e.Conclusion, e.CheckName, e.URL = event.ChecksDone, w.HeadBranch, w.HeadSHA, w.Conclusion, w.Name, w.HTMLURL
		e.Subject = "commit:" + w.HeadSHA
	case "check_suite": // other CI apps; Actions is reported by workflow_run, with workflow names
		cs := p.CheckSuite
		if p.Action != "completed" || cs == nil || cs.App.Slug == "github-actions" {
			return nil, nil
		}
		e.Kind, e.Branch, e.HeadSHA, e.Conclusion, e.CheckName = event.ChecksDone, cs.HeadBranch, cs.HeadSHA, cs.Conclusion, cs.App.Name
		e.Subject = "commit:" + cs.HeadSHA
	case "push":
		branch, ok := strings.CutPrefix(p.Ref, "refs/heads/")
		if !ok || p.Deleted {
			return nil, nil // tags and deleted branches
		}
		e.Kind, e.Branch, e.HeadSHA, e.Commits, e.URL = event.Push, branch, p.After, len(p.Commits), p.Compare
		e.Subject = "branch:" + branch
	}
	if e.Kind == "" || e.Repo == "" {
		return nil, nil
	}
	// GitHub redelivers with the same delivery id; anything else is a new
	// happening, even the same label added again.
	e.Occurrence = delivery
	if e.Occurrence == "" {
		e.Occurrence = fmt.Sprintf("%s:%s:%s:%s", e.Kind, e.Subject, e.HeadSHA, e.Label)
	}
	return e, nil
}

func setThread(e *event.Event, t *thread, isPR bool) {
	if t == nil {
		return
	}
	e.Number, e.Title, e.Body, e.URL, e.Author = t.Number, t.Title, t.Body, t.HTMLURL, t.User.Login
	for _, l := range t.Labels {
		e.Labels = append(e.Labels, l.Name)
	}
	e.Subject = fmt.Sprintf("issue:%d", t.Number)
	if isPR {
		e.Subject = fmt.Sprintf("pr:%d", t.Number)
		e.Draft, e.Branch, e.Base, e.HeadSHA = t.Draft, t.Head.Ref, t.Base.Ref, t.Head.SHA
		e.Fork = t.Head.Repo.FullName != "" && !strings.EqualFold(t.Head.Repo.FullName, e.Repo)
	}
}

// fillPullRequest adds a pull request's branches to an event about it, as
// the server's owner; it's best effort.
func (c *Client) fillPullRequest(ctx context.Context, e *event.Event) {
	tok, err := c.bearer(ctx)
	if err != nil || tok == "" {
		return
	}
	var t thread
	if c.call(ctx, tok, "GET", fmt.Sprintf("/repos/%s/pulls/%d", e.Repo, e.Number), nil, &t) != nil {
		return
	}
	e.Draft, e.Branch, e.Base, e.HeadSHA = t.Draft, t.Head.Ref, t.Base.Ref, t.Head.SHA
	e.Fork = t.Head.Repo.FullName != "" && !strings.EqualFold(t.Head.Repo.FullName, e.Repo)
}
