package github_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brdsmth/beethoven/internal/event"
	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/github"
)

const repo = `"repository": {"full_name": "acme/app"}, "sender": {"login": "jane", "type": "User"}`

func TestNormalize(t *testing.T) {
	for _, c := range []struct {
		kind, body string
		check      func(e *event.Event) bool
	}{
		{"issues", `{"action": "labeled", "label": {"name": "agent"}, "issue": {"number": 7, "title": "Crash", "labels": [{"name": "bug"}, {"name": "agent"}],
			"user": {"login": "bob"}}, ` + repo + `}`,
			func(e *event.Event) bool {
				return e.Kind == event.IssueLabeled && e.Label == "agent" && len(e.Labels) == 2 && e.Subject == "issue:7" && e.Author == "bob" && e.Actor == "jane"
			}},
		{"pull_request", `{"action": "opened", "pull_request": {"number": 3, "draft": true, "head": {"ref": "feature", "sha": "abc",
			"repo": {"full_name": "fork/app"}}, "base": {"ref": "main"}}, ` + repo + `}`,
			func(e *event.Event) bool {
				return e.Kind == event.PROpened && e.Branch == "feature" && e.Base == "main" && e.Draft && e.Fork && e.Subject == "pr:3"
			}},
		{"pull_request", `{"action": "closed", "pull_request": {"number": 3, "merged": true}, ` + repo + `}`,
			func(e *event.Event) bool { return e.Kind == event.PRMerged }},
		{"issue_comment", `{"action": "created", "issue": {"number": 3, "pull_request": {}}, "comment": {"body": "@agent fix it"}, ` + repo + `}`,
			func(e *event.Event) bool {
				return e.Kind == event.PRComment && e.Comment == "@agent fix it" && e.Subject == "pr:3"
			}},
		{"issue_comment", `{"action": "created", "issue": {"number": 9}, "comment": {"body": "hi"}, ` + repo + `}`,
			func(e *event.Event) bool { return e.Kind == event.IssueComment && e.Subject == "issue:9" }},
		{"pull_request_review", `{"action": "submitted", "review": {"state": "CHANGES_REQUESTED", "body": "no"}, "pull_request": {"number": 3}, ` + repo + `}`,
			func(e *event.Event) bool { return e.Kind == event.PRReview && e.State == "changes_requested" }},
		{"workflow_run", `{"action": "completed", "workflow_run": {"name": "CI", "head_branch": "main", "head_sha": "abc", "conclusion": "failure"}, ` + repo + `}`,
			func(e *event.Event) bool {
				return e.Kind == event.ChecksDone && e.CheckName == "CI" && e.Conclusion == "failure"
			}},
		{"check_suite", `{"action": "completed", "check_suite": {"app": {"slug": "github-actions"}}, ` + repo + `}`,
			func(e *event.Event) bool { return e == nil }}, // reported by workflow_run
		{"push", `{"ref": "refs/heads/main", "after": "abc", "commits": [{}, {}], ` + repo + `}`,
			func(e *event.Event) bool { return e.Kind == event.Push && e.Branch == "main" && e.Commits == 2 }},
		{"push", `{"ref": "refs/tags/v1", ` + repo + `}`, func(e *event.Event) bool { return e == nil }},
		{"issues", `{"action": "edited", "issue": {"number": 1}, ` + repo + `}`, func(e *event.Event) bool { return e == nil }},
		{"ping", `{"zen": "hi"}`, func(e *event.Event) bool { return e == nil }},
	} {
		e, err := github.Normalize(c.kind, "d-1", []byte(c.body))
		if err != nil || !c.check(e) {
			t.Errorf("%s %s: %+v, %v", c.kind, c.body[:30], e, err)
		}
		if e != nil && (e.Host != "github" || e.Repo != "acme/app" || e.Occurrence != "d-1") {
			t.Errorf("%s: %+v", c.kind, e)
		}
	}
}

func TestWebhookSignature(t *testing.T) {
	c := client(t, fake.NewGitHub(t))
	body := `{"action": "opened", "issue": {"number": 1}, ` + repo + `}`
	deliver := func(secret string) (*event.Event, error) {
		r := httptest.NewRequest("POST", "/webhooks/github", strings.NewReader(body))
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		r.Header.Set("X-GitHub-Event", "issues")
		return c.ParseWebhook(r)
	}
	if _, err := deliver("s3cret"); err == nil {
		t.Error("accepted a delivery with no secret configured")
	}
	c.RepoWebhookSecret = "s3cret"
	if e, err := deliver("s3cret"); err != nil || e.Kind != event.IssueOpened {
		t.Errorf("signed delivery: %+v, %v", e, err)
	}
	if _, err := deliver("wrong"); !errors.Is(err, github.ErrBadSignature) {
		t.Errorf("forged delivery: %v", err)
	}
}
