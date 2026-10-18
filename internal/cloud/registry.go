package cloud

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/brdsmth/beethoven/internal/store"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Workspace is one customer's workspace, as the service records it.
type Workspace struct {
	ID             string     `json:"id"`
	Slug           string     `json:"slug"`
	Name           string     `json:"name"`
	Status         string     `json:"status"` // active or suspended
	Plan           string     `json:"plan"`
	Claimed        bool       `json:"claimed"`
	Migrations     int        `json:"migrations"`
	LastMigration  string     `json:"last_migration,omitempty"`
	MigratedAt     *time.Time `json:"migrated_at,omitempty"`
	MigrationError string     `json:"migration_error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`

	sealedKey []byte
}

const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
)

// registry is the service's own database.
type registry struct {
	db  *sql.DB
	key []byte // the master key: seals workspaces' keys
}

func openRegistry(path string, masterKey []byte) (*registry, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("cloud: the master key must be 32 bytes")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := store.Migrate(db, migrations, "migrations", nil); err != nil {
		db.Close()
		return nil, fmt.Errorf("cloud: %s: %w", path, err)
	}
	return &registry{db: db, key: masterKey}, nil
}

func (g *registry) Close() error { return g.db.Close() }

var slugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$`)

// reserved are subdomains no workspace may take.
var reserved = map[string]bool{
	"app": true, "admin": true, "api": true, "www": true, "mail": true, "status": true, "docs": true, "help": true,
	"support": true, "cloud": true, "beethoven": true, "static": true, "assets": true, "ws": true, "auth": true,
	"login": true, "signup": true, "billing": true, "blog": true, "dev": true, "staging": true,
}

// CheckSlug explains what's wrong with a workspace subdomain, if anything.
func CheckSlug(slug string) error {
	switch {
	case !slugPattern.MatchString(slug):
		return fmt.Errorf("%w: a workspace address is 3 to 30 lowercase letters, digits or dashes", store.ErrInvalid)
	case reserved[slug] || strings.Contains(slug, "--"):
		return fmt.Errorf("%w: %q is taken", store.ErrInvalid, slug)
	}
	return nil
}

const wsCols = `id, slug, name, status, plan, data_key, claim_hash != '', migrations, last_migration, migrated_at,
	migration_error, created_at`

func scanWorkspace(row interface{ Scan(...any) error }) (Workspace, error) {
	var w Workspace
	var migrated sql.NullInt64
	var created int64
	err := row.Scan(&w.ID, &w.Slug, &w.Name, &w.Status, &w.Plan, &w.sealedKey, &w.Claimed, &w.Migrations,
		&w.LastMigration, &migrated, &w.MigrationError, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return w, store.ErrNotFound
	}
	w.Claimed = !w.Claimed // the column holds whether a claim is pending
	if migrated.Valid {
		t := time.UnixMilli(migrated.Int64)
		w.MigratedAt = &t
	}
	w.CreatedAt = time.UnixMilli(created)
	return w, err
}

// ClaimFor is how long a workspace's claim link works.
const ClaimFor = 7 * 24 * time.Hour

// create records a new workspace with a fresh data key, and returns the
// code that claims it: whoever has it creates its first account.
func (g *registry) create(ctx context.Context, slug, name string) (Workspace, string, error) {
	slug, name = strings.ToLower(strings.TrimSpace(slug)), strings.TrimSpace(name)
	if err := CheckSlug(slug); err != nil {
		return Workspace{}, "", err
	}
	if name == "" || len(name) > 80 {
		return Workspace{}, "", fmt.Errorf("%w: a workspace needs a name of at most 80 characters", store.ErrInvalid)
	}
	key := make([]byte, 32)
	rand.Read(key)
	sealed, err := g.seal(key)
	if err != nil {
		return Workspace{}, "", err
	}
	code := token()
	now := time.Now()
	w := Workspace{ID: store.NewID(), Slug: slug, Name: name, Status: StatusActive, Plan: "trial", CreatedAt: now, sealedKey: sealed}
	_, err = g.db.ExecContext(ctx, `INSERT INTO workspaces (id, slug, name, status, plan, data_key, claim_hash, claim_expires,
		created_at) VALUES (?,?,?,?,?,?,?,?,?)`, w.ID, w.Slug, w.Name, w.Status, w.Plan, sealed, hash(code),
		now.Add(ClaimFor).UnixMilli(), now.UnixMilli())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return Workspace{}, "", fmt.Errorf("%w: %q is taken", store.ErrInvalid, slug)
	}
	return w, code, err
}

func (g *registry) workspace(ctx context.Context, id string) (Workspace, error) {
	return scanWorkspace(g.db.QueryRowContext(ctx, `SELECT `+wsCols+` FROM workspaces WHERE id=?`, id))
}

func (g *registry) bySlug(ctx context.Context, slug string) (Workspace, error) {
	return scanWorkspace(g.db.QueryRowContext(ctx, `SELECT `+wsCols+` FROM workspaces WHERE slug=?`, slug))
}

func (g *registry) list(ctx context.Context) ([]Workspace, error) {
	rows, err := g.db.QueryContext(ctx, `SELECT `+wsCols+` FROM workspaces ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		w, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (g *registry) setStatus(ctx context.Context, id, status string) error {
	if status != StatusActive && status != StatusSuspended {
		return fmt.Errorf("%w: status is active or suspended", store.ErrInvalid)
	}
	_, err := g.db.ExecContext(ctx, `UPDATE workspaces SET status=? WHERE id=?`, status, id)
	return err
}

// claim uses up a workspace's claim code, if it is the one, in time.
func (g *registry) claim(ctx context.Context, id, code string) bool {
	if code == "" {
		return false
	}
	res, err := g.db.ExecContext(ctx, `UPDATE workspaces SET claim_hash='', claim_expires=NULL
		WHERE id=? AND claim_hash=? AND claim_expires > ?`, id, hash(code), time.Now().UnixMilli())
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// reclaim issues a new claim code for a workspace nobody has claimed yet.
func (g *registry) reclaim(ctx context.Context, id string) (string, error) {
	code := token()
	res, err := g.db.ExecContext(ctx, `UPDATE workspaces SET claim_hash=?, claim_expires=? WHERE id=?`,
		hash(code), time.Now().Add(ClaimFor).UnixMilli(), id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", store.ErrNotFound
	}
	return code, nil
}

// migrated records how a workspace's database last opened: its applied
// migrations (applied says whether any were new), or why it failed.
func (g *registry) migrated(ctx context.Context, id string, names []string, applied bool, openErr error) error {
	if openErr != nil {
		_, err := g.db.ExecContext(ctx, `UPDATE workspaces SET migration_error=? WHERE id=?`, openErr.Error(), id)
		return err
	}
	last := ""
	if len(names) > 0 {
		last = names[len(names)-1]
	}
	q := `UPDATE workspaces SET migrations=?, last_migration=?, migration_error='' WHERE id=?`
	args := []any{len(names), last, id}
	if applied {
		q = `UPDATE workspaces SET migrations=?, last_migration=?, migration_error='', migrated_at=? WHERE id=?`
		args = []any{len(names), last, time.Now().UnixMilli(), id}
	}
	_, err := g.db.ExecContext(ctx, q, args...)
	return err
}

// Routes: which workspace an outside identifier belongs to.
const routeSlack = "slack" // a Slack workspace, by team id

// route records that an outside identifier belongs to a workspace. It is
// refused if it belongs to another.
func (g *registry) route(ctx context.Context, kind, external, workspaceID string) error {
	var owner string
	err := g.db.QueryRowContext(ctx, `SELECT workspace_id FROM routes WHERE kind=? AND external_id=?`, kind, external).Scan(&owner)
	switch {
	case err == nil && owner != workspaceID:
		return errors.New("that is connected to another Beethoven workspace already; remove it there first")
	case err == nil:
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	_, err = g.db.ExecContext(ctx, `INSERT INTO routes (kind, external_id, workspace_id, created_at) VALUES (?,?,?,?)`,
		kind, external, workspaceID, time.Now().UnixMilli())
	return err
}

func (g *registry) unroute(ctx context.Context, kind, external, workspaceID string) error {
	_, err := g.db.ExecContext(ctx, `DELETE FROM routes WHERE kind=? AND external_id=? AND workspace_id=?`, kind, external, workspaceID)
	return err
}

// routed finds the workspace an outside identifier belongs to.
func (g *registry) routed(ctx context.Context, kind, external string) (Workspace, error) {
	return scanWorkspace(g.db.QueryRowContext(ctx, `SELECT `+prefixed("w.", wsCols)+` FROM routes r
		JOIN workspaces w ON w.id = r.workspace_id WHERE r.kind=? AND r.external_id=?`, kind, external))
}

func prefixed(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = p + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

// dataKey unseals a workspace's encryption key.
func (g *registry) dataKey(w Workspace) ([]byte, error) { return g.open(w.sealedKey) }

func (g *registry) seal(plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(g.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	rand.Read(nonce)
	return gcm.Seal(nonce, nonce, plain, []byte("beethoven workspace key")), nil
}

func (g *registry) open(sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(g.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("cloud: a sealed key is too short")
	}
	return gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte("beethoven workspace key"))
}

func token() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
