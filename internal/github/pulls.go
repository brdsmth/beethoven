package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/brdsmth/beethoven/internal/access"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

// TrackPullRequests keeps the pull requests sessions open up to date until
// they are merged or closed, asking GitHub every interval. Polling needs no
// webhook, so it works on servers GitHub can't reach.
func (c *Client) TrackPullRequests(ctx context.Context, every time.Duration, log *slog.Logger) {
	for {
		if err := c.SyncPullRequests(ctx); err != nil && ctx.Err() == nil {
			log.Warn("github: syncing pull requests", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// SyncPullRequests starts tracking pull requests sessions opened since the
// last sync and refreshes the open ones.
func (c *Client) SyncPullRequests(ctx context.Context) error {
	if c.DB == nil {
		return nil
	}
	if _, err := c.DB.TrackNewPullRequests(ctx); err != nil {
		return err
	}
	open, err := c.DB.OpenPullRequests(ctx)
	if err != nil || len(open) == 0 {
		return err
	}
	// Each pull request is read as the person whose session opened it, who
	// can see its repository; failing that, as the server's owner.
	tokens := map[string]string{}
	tokenFor := func(sessionID string) string {
		person := ""
		if s, err := c.DB.Session(ctx, sessionID); err == nil {
			person = s.AccountID
		}
		if tok, ok := tokens[person]; ok {
			return tok
		}
		tok, err := c.bearer(access.For(ctx, person))
		if (err != nil || tok == "") && person != "" {
			tok, _ = c.bearer(ctx)
		}
		tokens[person] = tok
		return tok
	}
	var errs []error
	for _, p := range open {
		tok := tokenFor(p.SessionID)
		if tok == "" {
			continue // nothing to ask GitHub with yet
		}
		if err := c.syncPull(ctx, tok, p); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// syncPull refreshes one pull request. With the ETag of the last read,
// GitHub answers 304 when nothing changed, which it doesn't count against
// the rate limit.
func (c *Client) syncPull(ctx context.Context, tok string, p store.PullRequest) error {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/repos/%s/pulls/%d", c.api(), p.Repo, p.Number), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+tok)
	if p.ETag != "" {
		req.Header.Set("If-None-Match", p.ETag)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
	case http.StatusOK:
		var v struct {
			State     string     `json:"state"`
			Draft     bool       `json:"draft"`
			CreatedAt time.Time  `json:"created_at"`
			MergedAt  *time.Time `json:"merged_at"`
			ClosedAt  *time.Time `json:"closed_at"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v); err != nil {
			return fmt.Errorf("github: %s#%d: %w", p.Repo, p.Number, err)
		}
		p.State = "open"
		switch {
		case v.MergedAt != nil:
			p.State = "merged"
		case v.State == "closed":
			p.State = "closed"
		}
		p.Draft, p.OpenedAt, p.MergedAt, p.ClosedAt, p.ETag = v.Draft, v.CreatedAt, v.MergedAt, v.ClosedAt, resp.Header.Get("ETag")
	default:
		return fmt.Errorf("github: %s#%d: %s", p.Repo, p.Number, resp.Status)
	}
	p.SyncedAt = time.Now()
	return c.DB.SavePullRequest(ctx, p)
}

// PRStatus reads the latest pull request from head in repo, with whether it
// can merge and its checks. GitHub computes mergeability in the background
// after a push, so a first read may not have it yet; it is asked once more.
func (c *Client) PRStatus(ctx context.Context, repo, head string) (*session.PRStatus, error) {
	tok, err := c.bearer(ctx)
	if err != nil {
		return nil, err
	}
	owner, _, _ := strings.Cut(repo, "/")
	var found []struct {
		Number int `json:"number"`
	}
	q := url.Values{"head": {owner + ":" + head}, "state": {"all"}, "per_page": {"1"}}
	if err := c.call(ctx, tok, "GET", "/repos/"+repo+"/pulls?"+q.Encode(), nil, &found); err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, nil
	}
	var pr struct {
		HTMLURL        string     `json:"html_url"`
		Number         int        `json:"number"`
		State          string     `json:"state"`
		Draft          bool       `json:"draft"`
		MergedAt       *time.Time `json:"merged_at"`
		Mergeable      *bool      `json:"mergeable"`
		MergeableState string     `json:"mergeable_state"`
		Base           struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	path := fmt.Sprintf("/repos/%s/pulls/%d", repo, found[0].Number)
	for attempt := 0; ; attempt++ {
		if err := c.call(ctx, tok, "GET", path, nil, &pr); err != nil {
			return nil, err
		}
		if pr.Mergeable != nil || pr.State != "open" || attempt == 1 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.mergeableWait()):
		}
	}
	st := &session.PRStatus{URL: pr.HTMLURL, Number: pr.Number, State: pr.State, Draft: pr.Draft, Base: pr.Base.Ref,
		HeadSHA: pr.Head.SHA, Mergeable: pr.Mergeable, MergeableState: pr.MergeableState, Checks: []session.Check{}}
	if pr.MergedAt != nil {
		st.State = "merged"
	}

	var runs struct {
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if err := c.call(ctx, tok, "GET", "/repos/"+repo+"/commits/"+pr.Head.SHA+"/check-runs?per_page=100", nil, &runs); err != nil {
		return nil, err
	}
	for _, r := range runs.CheckRuns {
		st.Checks = append(st.Checks, session.Check{Name: r.Name, Status: r.Status, Conclusion: r.Conclusion})
	}
	// Older CI reports commit statuses instead of check runs.
	var combined struct {
		Statuses []struct {
			Context string `json:"context"`
			State   string `json:"state"` // pending, success, failure or error
		} `json:"statuses"`
	}
	if err := c.call(ctx, tok, "GET", "/repos/"+repo+"/commits/"+pr.Head.SHA+"/status", nil, &combined); err != nil {
		return nil, err
	}
	for _, s := range combined.Statuses {
		ch := session.Check{Name: s.Context, Status: "completed", Conclusion: s.State}
		if s.State == "pending" {
			ch = session.Check{Name: s.Context, Status: "in_progress"}
		}
		st.Checks = append(st.Checks, ch)
	}
	return st, nil
}

func (c *Client) mergeableWait() time.Duration {
	if c.MergeableWait > 0 {
		return c.MergeableWait
	}
	return 2 * time.Second
}
