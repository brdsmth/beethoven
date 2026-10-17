package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Workspace roles. Owners and admins manage the server: its people, teams,
// settings and integrations; only owners make or unmake owners.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// ValidRole reports whether r is a workspace role.
func ValidRole(r string) bool { return r == RoleOwner || r == RoleAdmin || r == RoleMember }

// ErrLastOwner refuses a change that would leave the server without an
// active owner.
var ErrLastOwner = fmt.Errorf("%w: the server needs at least one active owner", ErrInvalid)

// Account is a person who signs in to the web UI.
type Account struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Owner     bool      `json:"owner"`              // Role == RoleOwner
	Disabled  bool      `json:"disabled,omitempty"` // deactivated: can't sign in
	CreatedAt time.Time `json:"created_at"`
}

// Admin reports whether the account manages the server.
func (a Account) Admin() bool { return a.Role == RoleOwner || a.Role == RoleAdmin }

// HasAccounts reports whether anyone can sign in yet.
func (s *Store) HasAccounts(ctx context.Context) (bool, error) {
	var ok bool
	err := s.r.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM accounts)`).Scan(&ok)
	return ok, err
}

// CreateOwner creates the first account, which owns the server. Once any
// account exists it fails with ErrConflict.
func (s *Store) CreateOwner(ctx context.Context, a Account, passwordHash string) (Account, error) {
	a.ID, a.Role, a.Owner, a.CreatedAt = NewID(), RoleOwner, true, time.Now()
	res, err := s.w.ExecContext(ctx, `INSERT INTO accounts (id, email, name, password_hash, owner, role, created_at)
		SELECT ?, ?, ?, ?, 1, 'owner', ? WHERE NOT EXISTS (SELECT 1 FROM accounts)`,
		a.ID, a.Email, a.Name, passwordHash, ms(a.CreatedAt))
	if err != nil {
		return a, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return a, ErrConflict
	}
	return a, nil
}

const accountCols = `a.id, a.email, a.name, a.role, a.disabled_at IS NOT NULL, a.created_at`

func scanAccount(row scanner, extra ...any) (Account, error) {
	var a Account
	var created int64
	err := row.Scan(append([]any{&a.ID, &a.Email, &a.Name, &a.Role, &a.Disabled, &created}, extra...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.Owner = a.Role == RoleOwner
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

// ActiveAccountByEmail returns the active account with an email, if any.
func (s *Store) ActiveAccountByEmail(ctx context.Context, email string) (Account, error) {
	return scanAccount(s.r.QueryRowContext(ctx,
		`SELECT `+accountCols+` FROM accounts a WHERE a.email=? AND a.disabled_at IS NULL`, email))
}

func (s *Store) Account(ctx context.Context, id string) (Account, error) {
	return scanAccount(s.r.QueryRowContext(ctx, `SELECT `+accountCols+` FROM accounts a WHERE a.id=?`, id))
}

// Accounts lists everyone, owners first, then by name.
func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	return query(ctx, s.r, func(r scanner) (Account, error) { return scanAccount(r) },
		`SELECT `+accountCols+` FROM accounts a ORDER BY a.disabled_at IS NOT NULL,
		CASE a.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, lower(COALESCE(NULLIF(a.name,''), a.email))`)
}

// FirstOwner is the server's longest-standing active owner: who the server
// acts for when no person is named.
func (s *Store) FirstOwner(ctx context.Context) (Account, error) {
	return scanAccount(s.r.QueryRowContext(ctx, `SELECT `+accountCols+` FROM accounts a
		WHERE a.role='owner' AND a.disabled_at IS NULL ORDER BY a.created_at LIMIT 1`))
}

// UpdateAccount changes an account's role and whether it is deactivated.
// Deactivating signs it out everywhere. Leaving no active owner fails with
// ErrLastOwner.
func (s *Store) UpdateAccount(ctx context.Context, id, role string, disabled bool) (Account, error) {
	if !ValidRole(role) {
		return Account{}, fmt.Errorf("%w: role must be owner, admin or member", ErrInvalid)
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()
	var owners int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE role='owner' AND disabled_at IS NULL AND id != ?`,
		id).Scan(&owners); err != nil {
		return Account{}, err
	}
	if owners == 0 && (role != RoleOwner || disabled) {
		var wasOwner bool
		if err := tx.QueryRowContext(ctx, `SELECT role='owner' AND disabled_at IS NULL FROM accounts WHERE id=?`, id).
			Scan(&wasOwner); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Account{}, err
		}
		if wasOwner {
			return Account{}, ErrLastOwner
		}
	}
	var disabledAt any
	if disabled {
		disabledAt = ms(time.Now())
	}
	res, err := tx.ExecContext(ctx, `UPDATE accounts SET role=?, owner=?, disabled_at=CASE WHEN ? THEN COALESCE(disabled_at, ?)
		ELSE NULL END WHERE id=?`, role, role == RoleOwner, disabled, disabledAt, id)
	if err != nil {
		return Account{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Account{}, ErrNotFound
	}
	if disabled {
		if _, err := tx.ExecContext(ctx, `DELETE FROM logins WHERE account_id=?`, id); err != nil {
			return Account{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Account{}, err
	}
	return s.Account(ctx, id)
}

// UpdateProfile changes an account's name.
func (s *Store) UpdateProfile(ctx context.Context, id, name string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE accounts SET name=? WHERE id=?`, name, id)
	return err
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

// LoginAccount returns the active account a signed-in browser belongs to,
// unless its login has expired.
func (s *Store) LoginAccount(ctx context.Context, id string) (Account, error) {
	return scanAccount(s.r.QueryRowContext(ctx, `SELECT `+accountCols+` FROM logins l JOIN accounts a ON a.id = l.account_id
		WHERE l.id=? AND l.expires_at > ? AND a.disabled_at IS NULL`, id, ms(time.Now())))
}

// DeleteLogin signs a browser out.
func (s *Store) DeleteLogin(ctx context.Context, id string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM logins WHERE id=?`, id)
	return err
}

// ----------------------------------------------------------------- invites

// Invite is a link that creates an account. Its token is shown once, when
// it is made; only the token's hash is stored.
type Invite struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Email     string    `json:"email,omitempty"`
	TeamID    string    `json:"team_id,omitempty"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

const InviteFor = 7 * 24 * time.Hour

// secretToken returns a new random token with a prefix, and its hash.
func secretToken(prefix string) (token, hash string) {
	b := make([]byte, 24)
	rand.Read(b)
	token = prefix + base64.RawURLEncoding.EncodeToString(b)
	return token, TokenHash(token)
}

// TokenHash is how tokens (invites, API tokens) are stored and looked up.
func TokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// CreateInvite stores an invitation and returns its token.
func (s *Store) CreateInvite(ctx context.Context, in *Invite) (string, error) {
	if !ValidRole(in.Role) {
		return "", fmt.Errorf("%w: role must be owner, admin or member", ErrInvalid)
	}
	token, hash := secretToken("")
	in.ID, in.CreatedAt = hash[:16], time.Now()
	in.ExpiresAt = in.CreatedAt.Add(InviteFor)
	_, err := s.w.ExecContext(ctx, `INSERT INTO invites (id, role, email, team_id, created_by, created_at, expires_at)
		VALUES (?,?,?,?,?,?,?)`, hash, in.Role, in.Email, in.TeamID, in.CreatedBy, ms(in.CreatedAt), ms(in.ExpiresAt))
	return token, err
}

const inviteCols = `id, role, email, team_id, created_by, created_at, expires_at`

func scanInvite(row scanner) (Invite, error) {
	var in Invite
	var created, expires int64
	err := row.Scan(&in.ID, &in.Role, &in.Email, &in.TeamID, &in.CreatedBy, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return in, ErrNotFound
	}
	in.CreatedAt, in.ExpiresAt = fromMS(created), fromMS(expires)
	in.ID = in.ID[:min(16, len(in.ID))] // a prefix of the hash names it; the full hash never leaves the store
	return in, err
}

// Invite returns the open invitation a token belongs to.
func (s *Store) Invite(ctx context.Context, token string) (Invite, error) {
	return scanInvite(s.r.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM invites
		WHERE id=? AND used_at IS NULL AND expires_at > ?`, TokenHash(token), ms(time.Now())))
}

// Invites lists open invitations, newest first.
func (s *Store) Invites(ctx context.Context) ([]Invite, error) {
	return query(ctx, s.r, scanInvite, `SELECT `+inviteCols+` FROM invites WHERE used_at IS NULL AND expires_at > ?
		ORDER BY created_at DESC`, ms(time.Now()))
}

// DeleteInvite withdraws an invitation, named by its id.
func (s *Store) DeleteInvite(ctx context.Context, id string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM invites WHERE substr(id, 1, 16)=? AND used_at IS NULL`, id)
	return err
}

// AcceptInvite creates an account from an open invitation, once: the
// account gets the invitation's role and joins its team, if it names one
// that still exists.
func (s *Store) AcceptInvite(ctx context.Context, token string, a Account, passwordHash string) (Account, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return a, err
	}
	defer tx.Rollback()
	in, err := scanInvite(tx.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM invites
		WHERE id=? AND used_at IS NULL AND expires_at > ?`, TokenHash(token), ms(time.Now())))
	if err != nil {
		return a, err
	}
	if in.Email != "" && in.Email != a.Email {
		return a, fmt.Errorf("%w: this invitation is for %s", ErrInvalid, in.Email)
	}
	a.ID, a.Role, a.Owner, a.CreatedAt = NewID(), in.Role, in.Role == RoleOwner, time.Now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO accounts (id, email, name, password_hash, owner, role, created_at)
		VALUES (?,?,?,?,?,?,?)`, a.ID, a.Email, a.Name, passwordHash, a.Owner, a.Role, ms(a.CreatedAt)); err != nil {
		return a, conflict(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invites SET used_by=?, used_at=? WHERE id=?`,
		a.ID, ms(a.CreatedAt), TokenHash(token)); err != nil {
		return a, err
	}
	if in.TeamID != "" {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO team_members (team_id, account_id, role, created_at)
			SELECT id, ?, 'member', ? FROM teams WHERE id=? AND archived_at IS NULL`, a.ID, ms(a.CreatedAt), in.TeamID); err != nil {
			return a, err
		}
	}
	return a, tx.Commit()
}

// -------------------------------------------------------------- API tokens

// APIToken is a personal token for scripts and the CLI. Its value is shown
// once, when it is made.
type APIToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Hint       string     `json:"hint"` // the token's last characters
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// TokenPrefix starts every personal API token, so they're recognizable.
const TokenPrefix = "bt_"

// CreateAPIToken makes a token for an account and returns its value.
func (s *Store) CreateAPIToken(ctx context.Context, accountID, name string) (APIToken, string, error) {
	token, hash := secretToken(TokenPrefix)
	t := APIToken{ID: hash[:16], Name: name, Hint: token[len(token)-4:], CreatedAt: time.Now()}
	_, err := s.w.ExecContext(ctx, `INSERT INTO api_tokens (id, account_id, name, hint, created_at) VALUES (?,?,?,?,?)`,
		hash, accountID, name, t.Hint, ms(t.CreatedAt))
	return t, token, err
}

// APITokens lists an account's tokens, newest first.
func (s *Store) APITokens(ctx context.Context, accountID string) ([]APIToken, error) {
	return query(ctx, s.r, func(r scanner) (APIToken, error) {
		var t APIToken
		var created int64
		var used sql.NullInt64
		err := r.Scan(&t.ID, &t.Name, &t.Hint, &created, &used)
		t.ID = t.ID[:min(16, len(t.ID))]
		t.CreatedAt, t.LastUsedAt = fromMS(created), optMS(used)
		return t, err
	}, `SELECT id, name, hint, created_at, last_used_at FROM api_tokens WHERE account_id=? ORDER BY created_at DESC`, accountID)
}

// DeleteAPIToken revokes one of an account's tokens, named by its id.
func (s *Store) DeleteAPIToken(ctx context.Context, accountID, id string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM api_tokens WHERE account_id=? AND substr(id, 1, 16)=?`, accountID, id)
	return err
}

// APITokenAccount returns the active account a token belongs to, and notes
// that it was used.
func (s *Store) APITokenAccount(ctx context.Context, token string) (Account, error) {
	hash := TokenHash(token)
	a, err := scanAccount(s.r.QueryRowContext(ctx, `SELECT `+accountCols+` FROM api_tokens t
		JOIN accounts a ON a.id = t.account_id WHERE t.id=? AND a.disabled_at IS NULL`, hash))
	if err == nil {
		s.w.ExecContext(ctx, `UPDATE api_tokens SET last_used_at=? WHERE id=?`, ms(time.Now()), hash)
	}
	return a, err
}
