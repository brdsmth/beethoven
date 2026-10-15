package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Account is a person who signs in to the web UI.
type Account struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Owner     bool      `json:"owner"`
	CreatedAt time.Time `json:"created_at"`
}

// HasAccounts reports whether anyone can sign in yet.
func (s *Store) HasAccounts(ctx context.Context) (bool, error) {
	var ok bool
	err := s.r.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM accounts)`).Scan(&ok)
	return ok, err
}

// CreateOwner creates the first account, which owns the server. Once any
// account exists it fails with ErrConflict.
func (s *Store) CreateOwner(ctx context.Context, a Account, passwordHash string) (Account, error) {
	a.ID, a.Owner, a.CreatedAt = NewID(), true, time.Now()
	res, err := s.w.ExecContext(ctx, `INSERT INTO accounts (id, email, name, password_hash, owner, created_at)
		SELECT ?, ?, ?, ?, 1, ? WHERE NOT EXISTS (SELECT 1 FROM accounts)`,
		a.ID, a.Email, a.Name, passwordHash, ms(a.CreatedAt))
	if err != nil {
		return a, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return a, ErrConflict
	}
	return a, nil
}

const accountCols = `a.id, a.email, a.name, a.owner, a.created_at`

func scanAccount(row scanner, extra ...any) (Account, error) {
	var a Account
	var created int64
	err := row.Scan(append([]any{&a.ID, &a.Email, &a.Name, &a.Owner, &created}, extra...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.CreatedAt = fromMS(created)
	return a, err
}

// AccountByEmail returns an account and its password hash.
func (s *Store) AccountByEmail(ctx context.Context, email string) (Account, string, error) {
	var hash string
	a, err := scanAccount(s.r.QueryRowContext(ctx,
		`SELECT `+accountCols+`, a.password_hash FROM accounts a WHERE a.email=?`, email), &hash)
	return a, hash, err
}

// CreateLogin records a signed-in browser by the hash of its cookie, and
// forgets expired ones.
func (s *Store) CreateLogin(ctx context.Context, id, accountID string, expires time.Time) error {
	now := time.Now()
	if _, err := s.w.ExecContext(ctx, `DELETE FROM logins WHERE expires_at <= ?`, ms(now)); err != nil {
		return err
	}
	_, err := s.w.ExecContext(ctx, `INSERT INTO logins (id, account_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		id, accountID, ms(now), ms(expires))
	return err
}

// LoginAccount returns the account a signed-in browser belongs to, unless
// its login has expired.
func (s *Store) LoginAccount(ctx context.Context, id string) (Account, error) {
	return scanAccount(s.r.QueryRowContext(ctx, `SELECT `+accountCols+` FROM logins l JOIN accounts a ON a.id = l.account_id
		WHERE l.id=? AND l.expires_at > ?`, id, ms(time.Now())))
}

// DeleteLogin signs a browser out.
func (s *Store) DeleteLogin(ctx context.Context, id string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM logins WHERE id=?`, id)
	return err
}
