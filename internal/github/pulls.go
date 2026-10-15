package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

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
	tok, err := c.bearer(ctx)
	if err != nil || tok == "" {
		return err // nothing to ask GitHub with yet
	}
	var errs []error
	for _, p := range open {
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
