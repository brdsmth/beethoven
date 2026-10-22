package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return a, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO accounts (id, email, name, password_hash, owner, role, created_at)
		SELECT ?, ?, ?, ?, 1, 'owner', ? WHERE NOT EXISTS (SELECT 1 FROM accounts)`,
		a.ID, a.Email, a.Name, passwordHash, ms(a.CreatedAt))
	if err != nil {
		return a, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return a, ErrConflict
	}
	if err := createPersonalTeam(ctx, tx, a.ID, a.CreatedAt); err != nil {
		return a, err
	}
	return a, tx.Commit()
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

// Invite is a link that creates an account, or adds someone who already
// has one to its team. Its token is shown once, when it is made; only the
// token's hash is stored.
type Invite struct {
	ID            string     `json:"id"`
	Role          string     `json:"role"`
	Email         string     `json:"email,omitempty"`
	TeamID        string     `json:"team_id,omitempty"`
	Note          string     `json:"note,omitempty"`           // from whoever sent it, shown on the invitation
	FirstPrompt   string     `json:"first_prompt,omitempty"`   // a first session to suggest
	EnvironmentID string     `json:"environment_id,omitempty"` // the project it runs in
	CreatedBy     string     `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	UsedAt        *time.Time `json:"used_at,omitempty"`
}

// Open reports whether the invitation can still be used.
func (in Invite) Open() bool { return in.UsedAt == nil && time.Now().Before(in.ExpiresAt) }

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
	_, err := s.w.ExecContext(ctx, `INSERT INTO invites (id, role, email, team_id, note, first_prompt, environment_id,
		created_by, created_at, expires_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, hash, in.Role, in.Email, in.TeamID, in.Note,
		in.FirstPrompt, in.EnvironmentID, in.CreatedBy, ms(in.CreatedAt), ms(in.ExpiresAt))
	return token, err
}

const inviteCols = `id, role, email, team_id, note, first_prompt, environment_id, created_by, created_at, expires_at, used_at`

func scanInvite(row scanner) (Invite, error) {
	var in Invite
	var created, expires int64
	var used sql.NullInt64
	err := row.Scan(&in.ID, &in.Role, &in.Email, &in.TeamID, &in.Note, &in.FirstPrompt, &in.EnvironmentID, &in.CreatedBy,
		&created, &expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return in, ErrNotFound
	}
	in.CreatedAt, in.ExpiresAt, in.UsedAt = fromMS(created), fromMS(expires), optMS(used)
	in.ID = in.ID[:min(16, len(in.ID))] // a prefix of the hash names it; the full hash never leaves the store
	return in, err
}

// Invite returns the open invitation a token belongs to.
func (s *Store) Invite(ctx context.Context, token string) (Invite, error) {
	return scanInvite(s.r.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM invites
		WHERE id=? AND used_at IS NULL AND expires_at > ?`, TokenHash(token), ms(time.Now())))
}

// AnyInvite returns the invitation a token belongs to, open or not, so its
// page can say what became of it.
func (s *Store) AnyInvite(ctx context.Context, token string) (Invite, error) {
	return scanInvite(s.r.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM invites WHERE id=?`, TokenHash(token)))
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

// openInvite is the open invitation a token belongs to, within tx, checked
// against the email of whoever uses it.
func openInvite(ctx context.Context, tx *sql.Tx, token, email string) (Invite, error) {
	in, err := scanInvite(tx.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM invites
		WHERE id=? AND used_at IS NULL AND expires_at > ?`, TokenHash(token), ms(time.Now())))
	if err != nil {
		return in, err
	}
	if in.Email != "" && in.Email != email {
		return in, fmt.Errorf("%w: this invitation is for %s", ErrInvalid, in.Email)
	}
	return in, nil
}

// useInvite marks an invitation used by an account, adds the account to
// its team, if it names one that still exists, and starts the account's
// getting started with what the invitation suggests: a new account from
// the start, one that's been here keeping its progress. It reports whether
// the account joined the team.
func useInvite(ctx context.Context, tx *sql.Tx, in Invite, token, accountID string, isNew bool) (bool, error) {
	at := time.Now()
	now := ms(at)
	if _, err := tx.ExecContext(ctx, `UPDATE invites SET used_by=?, used_at=? WHERE id=?`, accountID, now, TokenHash(token)); err != nil {
		return false, err
	}
	joined := false
	if in.TeamID != "" && !IsPersonalTeam(in.TeamID) {
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO team_members (team_id, account_id, role, created_at)
			SELECT id, ?, 'member', ? FROM teams WHERE id=? AND archived_at IS NULL`, accountID, now, in.TeamID)
		if err != nil {
			return false, err
		}
		n, _ := res.RowsAffected()
		joined = n > 0
	}
	o := Onboarding{InvitedBy: in.CreatedBy, Prompt: in.FirstPrompt, EnvironmentID: in.EnvironmentID, Tour: isNew, Since: at}
	if !isNew {
		var was string
		if err := tx.QueryRowContext(ctx, `SELECT onboarding FROM accounts WHERE id=?`, accountID).Scan(&was); err != nil {
			return false, err
		}
		var before Onboarding
		// Done with getting started, or started a session in it: they've had a first session.
		o.Started = was == "" || json.Unmarshal([]byte(was), &before) == nil && before.Started
		o.Tour = before.Tour
	}
	if joined {
		o.TeamID = in.TeamID
		// Their work goes to the team they were invited to, unless they
		// already chose a team beyond their personal one.
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET default_team_id=? WHERE id=? AND default_team_id IN ('', ?)`,
			in.TeamID, accountID, PersonalTeamID(accountID)); err != nil {
			return false, err
		}
	}
	b, _ := json.Marshal(o)
	_, err := tx.ExecContext(ctx, `UPDATE accounts SET onboarding=? WHERE id=?`, string(b), accountID)
	return joined, err
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
	in, err := openInvite(ctx, tx, token, a.Email)
	if err != nil {
		return a, err
	}
	a.ID, a.Role, a.Owner, a.CreatedAt = NewID(), in.Role, in.Role == RoleOwner, time.Now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO accounts (id, email, name, password_hash, owner, role, created_at)
		VALUES (?,?,?,?,?,?,?)`, a.ID, a.Email, a.Name, passwordHash, a.Owner, a.Role, ms(a.CreatedAt)); err != nil {
		return a, conflict(err)
	}
	// The invitation's team, if it has one, then becomes where their work goes.
	if err := createPersonalTeam(ctx, tx, a.ID, a.CreatedAt); err != nil {
		return a, err
	}
	if _, err := useInvite(ctx, tx, in, token, a.ID, true); err != nil {
		return a, err
	}
	return a, tx.Commit()
}

// JoinInvite uses an open invitation for someone who already has an
// account: it adds them to the invitation's team, and leaves their role as
// it is. It returns the team; an invitation without one, or whose team is
// archived or already has them, fails with ErrInvalid.
func (s *Store) JoinInvite(ctx context.Context, token string, a Account) (string, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	in, err := openInvite(ctx, tx, token, a.Email)
	if err != nil {
		return "", err
	}
	if in.TeamID == "" {
		return "", fmt.Errorf("%w: you already have an account, and this invitation doesn't add you to a team", ErrInvalid)
	}
	joined, err := useInvite(ctx, tx, in, token, a.ID, false)
	if err != nil {
		return "", err
	}
	if !joined {
		return "", fmt.Errorf("%w: you're already in that team, or it was archived", ErrInvalid)
	}
	return in.TeamID, tx.Commit()
}

// ---------------------------------------------------------- getting started

// Onboarding is where someone stands in getting started: who invited them,
// to which team, and what they suggested for a first session.
type Onboarding struct {
	TeamID        string    `json:"team_id,omitempty"`
	InvitedBy     string    `json:"invited_by,omitempty"` // an account
	Prompt        string    `json:"prompt,omitempty"`     // a first session the invitation suggests
	EnvironmentID string    `json:"environment_id,omitempty"`
	Tour          bool      `json:"tour,omitempty"`    // the welcome tour is still to show
	Started       bool      `json:"started,omitempty"` // they started a session
	Since         time.Time `json:"since"`             // when it began: they joined, or joined the team
}

// Onboarding returns where an account stands in getting started, or nil
// once it's done.
func (s *Store) Onboarding(ctx context.Context, accountID string) (*Onboarding, error) {
	var v string
	if err := s.r.QueryRowContext(ctx, `SELECT onboarding FROM accounts WHERE id=?`, accountID).Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if v == "" {
		return nil, nil
	}
	var o Onboarding
	if err := json.Unmarshal([]byte(v), &o); err != nil {
		return nil, nil // unreadable: treat it as done
	}
	return &o, nil
}

// OnboardingStep records progress in an account's getting started, if it's
// getting started: "tour" (the tour was seen), "started" (a session was
// started) or "done". Each is one statement, so steps can't undo each other.
func (s *Store) OnboardingStep(ctx context.Context, accountID, step string) error {
	q := map[string]string{
		"tour":    `json_remove(onboarding, '$.tour')`,
		"started": `json_set(onboarding, '$.started', json('true'))`,
		"done":    `''`,
	}[step]
	if q == "" {
		return fmt.Errorf("%w: no such step %q", ErrInvalid, step)
	}
	_, err := s.w.ExecContext(ctx, `UPDATE accounts SET onboarding=`+q+` WHERE id=? AND onboarding != ''`, accountID)
	return err
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
