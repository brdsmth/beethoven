package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Link roles.
const (
	LinkOwner    = "owner"    // the session opened the thread: its pull request
	LinkFollower = "follower" // it commented, was started for it, or asked to follow
)

// Link is a session taking part in a thread elsewhere.
type Link struct {
	Subject   string     `json:"subject"` // "github:owner/name:pr:42"
	SessionID string     `json:"session_id"`
	Role      string     `json:"role"`
	Reason    string     `json:"reason"`
	Muted     bool       `json:"muted"`
	CreatedAt time.Time  `json:"created_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

const linkCols = `subject, session_id, role, reason, muted, created_at, closed_at`

func scanLink(r scanner) (Link, error) {
	var l Link
	var created int64
	var closed sql.NullInt64
	err := r.Scan(&l.Subject, &l.SessionID, &l.Role, &l.Reason, &l.Muted, &created, &closed)
	l.CreatedAt, l.ClosedAt = fromMS(created), optMS(closed)
	return l, err
}

// AddLink makes a session take part in a thread, or again if the thread had
// ended. An owner stays an owner; muting is kept.
func (s *Store) AddLink(ctx context.Context, l Link) error {
	if l.Role != LinkOwner {
		l.Role = LinkFollower
	}
	_, err := s.w.ExecContext(ctx, `INSERT INTO session_links (subject, session_id, role, reason, created_at) VALUES (?,?,?,?,?)
		ON CONFLICT (subject, session_id) DO UPDATE SET closed_at = NULL,
		  role = CASE WHEN excluded.role = 'owner' THEN 'owner' ELSE session_links.role END`,
		l.Subject, l.SessionID, l.Role, l.Reason, ms(time.Now()))
	return err
}

// ThreadLinks lists the sessions taking part in a thread that hasn't ended.
func (s *Store) ThreadLinks(ctx context.Context, subject string) ([]Link, error) {
	return query(ctx, s.r, scanLink, `SELECT `+linkCols+` FROM session_links WHERE subject=? AND closed_at IS NULL
		ORDER BY created_at`, subject)
}

// SessionLinks lists the threads a session takes part in, ended ones too.
func (s *Store) SessionLinks(ctx context.Context, sessionID string) ([]Link, error) {
	return query(ctx, s.r, scanLink, `SELECT `+linkCols+` FROM session_links WHERE session_id=? ORDER BY created_at`, sessionID)
}

// MuteLink stops or resumes what happens in a thread coming to a session.
func (s *Store) MuteLink(ctx context.Context, subject, sessionID string, muted bool) error {
	res, err := s.w.ExecContext(ctx, `UPDATE session_links SET muted=? WHERE subject=? AND session_id=?`, muted, subject, sessionID)
	return affected(res, err)
}

// RemoveLink takes a session out of a thread.
func (s *Store) RemoveLink(ctx context.Context, subject, sessionID string) error {
	res, err := s.w.ExecContext(ctx, `DELETE FROM session_links WHERE subject=? AND session_id=?`, subject, sessionID)
	return affected(res, err)
}

// EndThread marks a thread ended for every session in it.
func (s *Store) EndThread(ctx context.Context, subject string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE session_links SET closed_at=? WHERE subject=? AND closed_at IS NULL`, ms(time.Now()), subject)
	return err
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Outbound is something a session posted elsewhere.
type Outbound struct {
	Ref       string // the comment's URL
	SessionID string
	PromptID  string
	Hop       int // of the prompt it was posted during
	CreatedAt time.Time
}

func (s *Store) AddOutbound(ctx context.Context, o Outbound) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO outbound (ref, session_id, prompt_id, hop, created_at) VALUES (?,?,?,?,?)
		ON CONFLICT (ref) DO NOTHING`, o.Ref, o.SessionID, o.PromptID, o.Hop, ms(time.Now()))
	return err
}

// OutboundByRef finds what a session posted by its URL.
func (s *Store) OutboundByRef(ctx context.Context, ref string) (Outbound, error) {
	var o Outbound
	var created int64
	err := s.r.QueryRowContext(ctx, `SELECT ref, session_id, prompt_id, hop, created_at FROM outbound WHERE ref=?`, ref).
		Scan(&o.Ref, &o.SessionID, &o.PromptID, &o.Hop, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return o, ErrNotFound
	}
	o.CreatedAt = fromMS(created)
	return o, err
}

// InboxItem is something that happened in a session's thread, for it.
type InboxItem struct {
	ID          int64           `json:"id"`
	SessionID   string          `json:"session_id"`
	Subject     string          `json:"subject"`
	Occurrence  string          `json:"occurrence"`
	Kind        string          `json:"kind"`
	Actor       string          `json:"actor"`
	Data        json.RawMessage `json:"data"`
	ReceivedAt  time.Time       `json:"received_at"`
	DeliveredAt *time.Time      `json:"delivered_at,omitempty"`
	PromptID    string          `json:"prompt_id,omitempty"`
	Outcome     string          `json:"outcome,omitempty"`
}

const inboxCols = `id, session_id, subject, occurrence, kind, actor, data, received_at, delivered_at, prompt_id, outcome`

func scanInbox(r scanner) (InboxItem, error) {
	var x InboxItem
	var data string
	var received int64
	var delivered sql.NullInt64
	err := r.Scan(&x.ID, &x.SessionID, &x.Subject, &x.Occurrence, &x.Kind, &x.Actor, &data, &received, &delivered, &x.PromptID, &x.Outcome)
	x.Data, x.ReceivedAt, x.DeliveredAt = json.RawMessage(data), fromMS(received), optMS(delivered)
	return x, err
}

// AddToInbox queues an item for a session, once per occurrence; added is
// false for one already there.
func (s *Store) AddToInbox(ctx context.Context, x InboxItem) (added bool, err error) {
	res, err := s.w.ExecContext(ctx, `INSERT INTO inbox (session_id, subject, occurrence, kind, actor, data, received_at)
		VALUES (?,?,?,?,?,?,?) ON CONFLICT (session_id, occurrence) DO NOTHING`,
		x.SessionID, x.Subject, x.Occurrence, x.Kind, x.Actor, string(x.Data), ms(time.Now()))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// PendingInbox lists undelivered items, oldest first.
func (s *Store) PendingInbox(ctx context.Context) ([]InboxItem, error) {
	return query(ctx, s.r, scanInbox, `SELECT `+inboxCols+` FROM inbox WHERE delivered_at IS NULL ORDER BY id`)
}

// SessionInbox lists a session's latest items, newest first.
func (s *Store) SessionInbox(ctx context.Context, sessionID string, limit int) ([]InboxItem, error) {
	return query(ctx, s.r, scanInbox, `SELECT `+inboxCols+` FROM inbox WHERE session_id=? ORDER BY id DESC LIMIT ?`, sessionID, limit)
}

// SettleInbox records what became of items: the prompt they were delivered
// in, or why they weren't.
func (s *Store) SettleInbox(ctx context.Context, ids []int64, promptID, outcome string) error {
	if len(ids) == 0 {
		return nil
	}
	args := []any{ms(time.Now()), promptID, outcome}
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.w.ExecContext(ctx, `UPDATE inbox SET delivered_at=?, prompt_id=?, outcome=? WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	return err
}

// DeliveredSince counts the prompts a session was handed from its inbox
// since a time.
func (s *Store) DeliveredSince(ctx context.Context, sessionID string, since time.Time) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(DISTINCT prompt_id) FROM inbox WHERE session_id=? AND prompt_id != '' AND delivered_at >= ?`,
		sessionID, ms(since)).Scan(&n)
	return n, err
}

// SessionsOnBranch lists the sessions working on a branch, newest first.
func (s *Store) SessionsOnBranch(ctx context.Context, branch string) ([]Session, error) {
	return query(ctx, s.r, scanSession, `SELECT `+sessionCols+` FROM sessions WHERE branch=? ORDER BY created_at DESC`, branch)
}

// PullRequest finds a tracked pull request.
func (s *Store) PullRequest(ctx context.Context, repo string, number int) (PullRequest, error) {
	p, err := scanPull(s.r.QueryRowContext(ctx, `SELECT `+pullCols+` FROM pull_requests WHERE repo=? COLLATE NOCASE AND number=?`, repo, number))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}
