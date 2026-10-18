package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Artifact is a snapshot of a file shown in a session's viewer. Data is
// filled in only by Artifact; listing leaves it out.
type Artifact struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"session_id"`
	PromptID    string    `json:"prompt_id,omitempty"`
	Path        string    `json:"path"` // where it was in the sandbox
	Name        string    `json:"name"`
	Title       string    `json:"title,omitempty"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	Data        []byte    `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
}

const artifactCols = `id, session_id, prompt_id, path, name, title, content_type, size, created_at`

func scanArtifact(row scanner, extra ...any) (Artifact, error) {
	var a Artifact
	var created int64
	err := row.Scan(append([]any{&a.ID, &a.SessionID, &a.PromptID, &a.Path, &a.Name, &a.Title, &a.ContentType, &a.Size, &created}, extra...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.CreatedAt = fromMS(created)
	return a, err
}

func (s *Store) CreateArtifact(ctx context.Context, a *Artifact) error {
	if a.ID == "" {
		a.ID = NewID()
	}
	if a.Data == nil {
		a.Data = []byte{}
	}
	a.Size, a.CreatedAt = int64(len(a.Data)), time.Now()
	_, err := s.w.ExecContext(ctx, `INSERT INTO artifacts (`+artifactCols+`, data) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.SessionID, a.PromptID, a.Path, a.Name, a.Title, a.ContentType, a.Size, ms(a.CreatedAt), a.Data)
	return err
}

// Artifacts lists a session's artifacts, oldest first, without their data.
func (s *Store) Artifacts(ctx context.Context, sessionID string) ([]Artifact, error) {
	return query(ctx, s.r, func(r scanner) (Artifact, error) { return scanArtifact(r) },
		`SELECT `+artifactCols+` FROM artifacts WHERE session_id=? ORDER BY created_at, rowid`, sessionID)
}

// Artifact returns one of a session's artifacts with its content.
func (s *Store) Artifact(ctx context.Context, sessionID, id string) (Artifact, error) {
	var data []byte
	a, err := scanArtifact(s.r.QueryRowContext(ctx, `SELECT `+artifactCols+`, data FROM artifacts WHERE session_id=? AND id=?`,
		sessionID, id), &data)
	a.Data = data
	return a, err
}
