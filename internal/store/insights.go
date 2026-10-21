package store

import (
	"context"
	"database/sql"
	"time"
)

// SessionsCreated lists the sessions created in [from, to), oldest first.
func (s *Store) SessionsCreated(ctx context.Context, from, to time.Time) ([]Session, error) {
	return query(ctx, s.r, scanSession, `SELECT `+sessionCols+` FROM sessions WHERE created_at >= ? AND created_at < ?
		ORDER BY created_at`, ms(from), ms(to))
}

// PromptsOfSessionsCreated lists the prompts of the sessions created in
// [from, to), in queue order.
func (s *Store) PromptsOfSessionsCreated(ctx context.Context, from, to time.Time) ([]Prompt, error) {
	return query(ctx, s.r, scanPrompt, `SELECT `+promptCols+` FROM prompts WHERE session_id IN
		(SELECT id FROM sessions WHERE created_at >= ? AND created_at < ?) ORDER BY created_at, rowid`, ms(from), ms(to))
}

// SessionsByID lists the sessions with the given ids that exist.
func (s *Store) SessionsByID(ctx context.Context, ids []string) ([]Session, error) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return query(ctx, s.r, scanSession, `SELECT `+sessionCols+` FROM sessions WHERE id IN (`+placeholders(len(ids))+`)`, args...)
}

// ToolUse counts a session's calls to one tool and how many failed.
type ToolUse struct {
	SessionID string
	Name      string
	Calls     int
	Errors    int
}

// ToolUseOfSessionsCreated counts the tool calls of the sessions created in
// [from, to), per session and tool.
func (s *Store) ToolUseOfSessionsCreated(ctx context.Context, from, to time.Time) ([]ToolUse, error) {
	const window = `session_id IN (SELECT id FROM sessions WHERE created_at >= ? AND created_at < ?)`
	return query(ctx, s.r, func(r scanner) (u ToolUse, err error) {
		return u, r.Scan(&u.SessionID, &u.Name, &u.Calls, &u.Errors)
	}, `SELECT t.session_id, t.name, COUNT(*), COALESCE(SUM(r.err), 0) FROM
		(SELECT session_id, json_extract(data, '$.id') AS id, COALESCE(json_extract(data, '$.name'), '') AS name
		 FROM events WHERE type='tool' AND `+window+`) t
		LEFT JOIN (SELECT session_id, json_extract(data, '$.id') AS id, MAX(COALESCE(json_extract(data, '$.is_error'), 0)) AS err
		 FROM events WHERE type='tool.result' AND `+window+` GROUP BY session_id, id) r
		ON r.session_id = t.session_id AND r.id = t.id
		GROUP BY t.session_id, t.name`, ms(from), ms(to), ms(from), ms(to))
}

// SandboxState is a session's sandbox entering a state.
type SandboxState struct {
	SessionID string
	State     string
	At        time.Time
}

// SandboxStatesOfSessionsCreated lists the sandbox states the sessions
// created in [from, to) went through, per session in order.
func (s *Store) SandboxStatesOfSessionsCreated(ctx context.Context, from, to time.Time) ([]SandboxState, error) {
	return query(ctx, s.r, func(r scanner) (x SandboxState, err error) {
		var at int64
		err = r.Scan(&x.SessionID, &x.State, &at)
		x.At = fromMS(at)
		return x, err
	}, `SELECT session_id, COALESCE(json_extract(data, '$.state'), ''), at FROM events
		WHERE type='sandbox' AND session_id IN (SELECT id FROM sessions WHERE created_at >= ? AND created_at < ?)
		ORDER BY session_id, seq`, ms(from), ms(to))
}

// InviteOutcome is how far the person an invitation brought in has got.
type InviteOutcome struct {
	TeamID       string
	CreatedAt    time.Time
	UsedAt       *time.Time
	FirstSession *time.Time // their first session, if any
	Merged       bool       // a session of theirs had a pull request merged
}

// InviteOutcomes lists the invitations created since from, oldest first.
func (s *Store) InviteOutcomes(ctx context.Context, from time.Time) ([]InviteOutcome, error) {
	return query(ctx, s.r, func(r scanner) (x InviteOutcome, err error) {
		var created int64
		var used, first sql.NullInt64
		err = r.Scan(&x.TeamID, &created, &used, &first, &x.Merged)
		x.CreatedAt, x.UsedAt, x.FirstSession = fromMS(created), optMS(used), optMS(first)
		return x, err
	}, `SELECT i.team_id, i.created_at, i.used_at,
		(SELECT MIN(s.created_at) FROM sessions s WHERE i.used_by != '' AND s.account_id = i.used_by),
		EXISTS (SELECT 1 FROM pull_requests p JOIN sessions s ON s.id = p.session_id
		        WHERE i.used_by != '' AND s.account_id = i.used_by AND p.state = 'merged')
		FROM invites i WHERE i.created_at >= ? ORDER BY i.created_at`, ms(from))
}
