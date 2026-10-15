package store

import (
	"context"
	"database/sql"
	"errors"
)

// Attachment is a file attached to a prompt. Data is filled in only by
// PromptFiles and Attachment; listing prompts leaves it out.
type Attachment struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Data        []byte `json:"-"`
}

const attachmentCols = `id, name, content_type, size`

func scanAttachment(row scanner, extra ...any) (Attachment, error) {
	var a Attachment
	err := row.Scan(append([]any{&a.ID, &a.Name, &a.ContentType, &a.Size}, extra...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// addAttachments stores p's attachments, in order, and drops their data
// from p: it may be large, and nothing needs it once stored.
func addAttachments(ctx context.Context, tx *sql.Tx, p *Prompt) error {
	for i := range p.Attachments {
		a := &p.Attachments[i]
		if a.ID == "" {
			a.ID = NewID()
		}
		if a.Data == nil {
			a.Data = []byte{} // the column is NOT NULL
		}
		a.Size = int64(len(a.Data))
		if _, err := tx.ExecContext(ctx, `INSERT INTO attachments (id, prompt_id, session_id, name, content_type, size,
			data) VALUES (?,?,?,?,?,?,?)`, a.ID, p.ID, p.SessionID, a.Name, a.ContentType, a.Size, a.Data); err != nil {
			return err
		}
		a.Data = nil
	}
	return nil
}

// promptAttachments lists the attachments of the prompts that where selects
// with args, without their data, by prompt.
func promptAttachments(ctx context.Context, db queryer, where string, args ...any) (map[string][]Attachment, error) {
	out := map[string][]Attachment{}
	_, err := query(ctx, db, func(r scanner) (struct{}, error) {
		var prompt string
		a, err := scanAttachment(r, &prompt)
		out[prompt] = append(out[prompt], a)
		return struct{}{}, err
	}, `SELECT `+attachmentCols+`, prompt_id FROM attachments WHERE `+where+` ORDER BY rowid`, args...)
	return out, err
}

// PromptFiles returns a prompt's attachments with their content.
func (s *Store) PromptFiles(ctx context.Context, promptID string) ([]Attachment, error) {
	return query(ctx, s.r, func(r scanner) (Attachment, error) {
		var data []byte
		a, err := scanAttachment(r, &data)
		a.Data = data
		return a, err
	}, `SELECT `+attachmentCols+`, data FROM attachments WHERE prompt_id=? ORDER BY rowid`, promptID)
}

// Attachment returns one of a session's attachments with its content.
func (s *Store) Attachment(ctx context.Context, sessionID, id string) (Attachment, error) {
	var data []byte
	a, err := scanAttachment(s.r.QueryRowContext(ctx, `SELECT `+attachmentCols+`, data FROM attachments
		WHERE session_id=? AND id=?`, sessionID, id), &data)
	a.Data = data
	return a, err
}
