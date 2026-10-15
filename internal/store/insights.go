package store

import (
	"context"
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
