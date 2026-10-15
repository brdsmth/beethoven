package store

import (
	"context"
	"database/sql"
	"regexp"
	"strconv"
	"time"
)

// PullRequest is a pull request a session opened, as last seen on GitHub.
type PullRequest struct {
	Repo      string     `json:"repo"`
	Number    int        `json:"number"`
	SessionID string     `json:"session_id"`
	URL       string     `json:"url"`
	State     string     `json:"state"` // open, merged or closed (without merging)
	Draft     bool       `json:"draft"`
	OpenedAt  time.Time  `json:"opened_at"`
	MergedAt  *time.Time `json:"merged_at,omitempty"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
	ETag      string     `json:"-"`
	SyncedAt  time.Time  `json:"synced_at"`
}

var pullURL = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/pull/(\d+)$`)

// TrackNewPullRequests starts tracking the pull requests sessions opened
// that are not tracked yet, returning how many it added.
func (s *Store) TrackNewPullRequests(ctx context.Context) (int, error) {
	rows, err := query(ctx, s.r, func(r scanner) (x [2]string, err error) { return x, r.Scan(&x[0], &x[1]) },
		`SELECT s.id, json_extract(j.value, '$.pr_url') FROM sessions s, json_each(s.repos) j
		 WHERE COALESCE(json_extract(j.value, '$.pr_url'), '') != ''
		   AND json_extract(j.value, '$.pr_url') NOT IN (SELECT url FROM pull_requests)`)
	if err != nil {
		return 0, err
	}
	added := 0
	for _, row := range rows {
		m := pullURL.FindStringSubmatch(row[1])
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		res, err := s.w.ExecContext(ctx, `INSERT INTO pull_requests (repo, number, session_id, url) VALUES (?,?,?,?)
			ON CONFLICT DO NOTHING`, m[1], n, row[0], row[1])
		if err != nil {
			return added, err
		}
		if c, _ := res.RowsAffected(); c > 0 {
			added++
		}
	}
	return added, nil
}

const pullCols = `repo, number, session_id, url, state, draft, opened_at, merged_at, closed_at, etag, synced_at`

func scanPull(r scanner) (PullRequest, error) {
	var p PullRequest
	var opened, synced int64
	var merged, closed sql.NullInt64
	err := r.Scan(&p.Repo, &p.Number, &p.SessionID, &p.URL, &p.State, &p.Draft, &opened, &merged, &closed, &p.ETag, &synced)
	if opened > 0 {
		p.OpenedAt = fromMS(opened)
	}
	if synced > 0 {
		p.SyncedAt = fromMS(synced)
	}
	p.MergedAt, p.ClosedAt = optMS(merged), optMS(closed)
	return p, err
}

// OpenPullRequests lists the tracked pull requests still open on GitHub,
// least recently synced first.
func (s *Store) OpenPullRequests(ctx context.Context) ([]PullRequest, error) {
	return query(ctx, s.r, scanPull, `SELECT `+pullCols+` FROM pull_requests WHERE state='open' ORDER BY synced_at`)
}

// SessionPullRequests lists a session's pull requests.
func (s *Store) SessionPullRequests(ctx context.Context, sessionID string) ([]PullRequest, error) {
	return query(ctx, s.r, scanPull, `SELECT `+pullCols+` FROM pull_requests WHERE session_id=? ORDER BY repo, number`, sessionID)
}

// PullRequestsOf lists the pull requests of sessions created in [from, to).
func (s *Store) PullRequestsOf(ctx context.Context, from, to time.Time) ([]PullRequest, error) {
	return query(ctx, s.r, scanPull, `SELECT `+pullCols+` FROM pull_requests WHERE session_id IN
		(SELECT id FROM sessions WHERE created_at >= ? AND created_at < ?)`, ms(from), ms(to))
}

// SavePullRequest stores what GitHub said about a pull request.
func (s *Store) SavePullRequest(ctx context.Context, p PullRequest) error {
	_, err := s.w.ExecContext(ctx, `UPDATE pull_requests SET state=?, draft=?, opened_at=?, merged_at=?, closed_at=?,
		etag=?, synced_at=? WHERE repo=? AND number=?`,
		p.State, p.Draft, ms(p.OpenedAt), optToMS(p.MergedAt), optToMS(p.ClosedAt), p.ETag, ms(p.SyncedAt), p.Repo, p.Number)
	return err
}
