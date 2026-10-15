package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Secrets are environment variables injected into sandboxes. Every secret
// belongs to a scope:
//
//	""                 global
//	"owner/name"       a repository
//	EnvScope(id)       an environment
//	CredentialScope    the server's own credentials (Anthropic, GitHub)
//
// A session receives the layers of its SecretTarget, later layers winning.

const CredentialScope = "@credentials"

func EnvScope(environmentID string) string { return "env:" + environmentID }

const (
	MaxSecretBytes     = 16 << 10
	MaxSecretsPerScope = 100
)

var secretName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Names the server sets itself: a secret may not shadow them.
var reservedNames = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "SHELL": true, "TERM": true, "PWD": true, "LANG": true,
	"HOSTNAME": true, "GH_TOKEN": true, "GH_TOKENS": true, "REPOS": true, "WORKDIR": true, "WORK_BRANCH": true, "BRANCH": true,
	"ENV_SETUP": true, "ENV_SETUP_ID": true, "ENV_START": true,
}
var reservedPrefixes = []string{"BEETHOVEN_", "GIT_AUTHOR_", "GIT_COMMITTER_"}

// ValidateSecret reports why name=value cannot be stored, if it cannot.
func ValidateSecret(name, value string) error {
	switch {
	case len(name) > 256 || !secretName.MatchString(name):
		return fmt.Errorf("%w: %q is not a valid environment variable name", ErrInvalid, name)
	case reservedNames[strings.ToUpper(name)]:
		return fmt.Errorf("%w: %s is set by the server and cannot be a secret", ErrInvalid, name)
	case len(value) > MaxSecretBytes:
		return fmt.Errorf("%w: %s is longer than %d bytes", ErrInvalid, name, MaxSecretBytes)
	}
	for _, p := range reservedPrefixes {
		if strings.HasPrefix(strings.ToUpper(name), p) {
			return fmt.Errorf("%w: names starting with %s are set by the server", ErrInvalid, p)
		}
	}
	return nil
}

type SecretInfo struct {
	Scope     string    `json:"scope"`
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SetSecret creates or replaces a secret.
func (s *Store) SetSecret(ctx context.Context, scope, name, value string) error {
	if err := ValidateSecret(name, value); err != nil {
		return err
	}
	var count, exists int
	err := s.w.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(name=?),0) FROM secrets WHERE scope=?`, name, scope).
		Scan(&count, &exists)
	if err != nil {
		return err
	}
	if exists == 0 && count >= MaxSecretsPerScope {
		return fmt.Errorf("%w: a scope holds at most %d secrets", ErrInvalid, MaxSecretsPerScope)
	}
	return s.put(ctx, scope, name, value)
}

func (s *Store) put(ctx context.Context, scope, name, value string) error {
	sealed, err := s.seal([]byte(value))
	if err != nil {
		return err
	}
	_, err = s.w.ExecContext(ctx, `INSERT INTO secrets (scope, name, value, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(scope, name) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		scope, name, sealed, ms(time.Now()))
	return err
}

func (s *Store) DeleteSecret(ctx context.Context, scope, name string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM secrets WHERE scope=? AND name=?`, scope, name)
	return err
}

// Secrets lists secret names, in one scope or (scope == nil) all of them
// except the server's credentials. Values never leave the store except
// through SecretEnv and Credentials.
func (s *Store) Secrets(ctx context.Context, scope *string) ([]SecretInfo, error) {
	q, args := `SELECT scope, name, updated_at FROM secrets WHERE scope != ?`, []any{CredentialScope}
	if scope != nil {
		q, args = `SELECT scope, name, updated_at FROM secrets WHERE scope = ?`, []any{*scope}
	}
	return query(ctx, s.r, func(r scanner) (x SecretInfo, err error) {
		var at int64
		err = r.Scan(&x.Scope, &x.Name, &at)
		x.UpdatedAt = fromMS(at)
		return x, err
	}, q+` ORDER BY scope, name`, args...)
}

// SecretTarget describes what a session works on, which decides its secrets.
type SecretTarget struct {
	EnvironmentID string
	InheritGlobal bool     // environment sessions: also take global secrets
	Repos         []string // primary first
}

// Layers lists the target's secret scopes from lowest to highest precedence:
// global (unless an environment opts out), then either the environment or
// the repositories, primary last so that it wins. An environment is a
// boundary: its repositories' own secrets do not apply.
func (t SecretTarget) Layers() []string {
	var layers []string
	if t.EnvironmentID == "" || t.InheritGlobal {
		layers = append(layers, "")
	}
	if t.EnvironmentID != "" {
		return append(layers, EnvScope(t.EnvironmentID))
	}
	for i := len(t.Repos) - 1; i >= 0; i-- {
		layers = append(layers, t.Repos[i])
	}
	return layers
}

type layered struct {
	scope, name string
	value       []byte
}

// layered loads the secrets of scopes, ordered so that later layers come last.
func (s *Store) layered(ctx context.Context, scopes []string) ([]layered, error) {
	if len(scopes) == 0 {
		return nil, nil
	}
	args := make([]any, len(scopes))
	rank := map[string]int{}
	for i, sc := range scopes {
		args[i], rank[sc] = sc, i
	}
	rows, err := query(ctx, s.r, func(r scanner) (x layered, err error) { return x, r.Scan(&x.scope, &x.name, &x.value) },
		`SELECT scope, name, value FROM secrets WHERE scope IN (?`+strings.Repeat(",?", len(scopes)-1)+`) ORDER BY name`, args...)
	sort.SliceStable(rows, func(i, j int) bool { return rank[rows[i].scope] < rank[rows[j].scope] })
	return rows, err
}

// SecretEnv returns the decrypted environment for a target.
func (s *Store) SecretEnv(ctx context.Context, t SecretTarget) (map[string]string, error) {
	return s.decrypt(ctx, t.Layers())
}

// Credentials returns the server's stored credentials.
func (s *Store) Credentials(ctx context.Context) (map[string]string, error) {
	return s.decrypt(ctx, []string{CredentialScope})
}

func (s *Store) decrypt(ctx context.Context, scopes []string) (map[string]string, error) {
	rows, err := s.layered(ctx, scopes)
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, r := range rows {
		v, err := s.open(r.value)
		if err != nil {
			return nil, err
		}
		env[r.name] = string(v)
	}
	return env, nil
}

// EffectiveSecret explains one variable a target receives.
type EffectiveSecret struct {
	Name      string   `json:"name"`
	Scope     string   `json:"scope"`               // the scope whose value wins
	Overrides []string `json:"overrides,omitempty"` // lower scopes it replaces
}

// EffectiveSecrets lists what a target receives and from where, without values.
func (s *Store) EffectiveSecrets(ctx context.Context, t SecretTarget) ([]EffectiveSecret, error) {
	rows, err := s.layered(ctx, t.Layers())
	if err != nil {
		return nil, err
	}
	byName := map[string]*EffectiveSecret{}
	var names []string
	for _, r := range rows {
		e, ok := byName[r.name]
		if !ok {
			e = &EffectiveSecret{Name: r.name}
			byName[r.name] = e
			names = append(names, r.name)
		} else {
			e.Overrides = append(e.Overrides, e.Scope)
		}
		e.Scope = r.scope
	}
	sort.Strings(names)
	out := make([]EffectiveSecret, 0, len(names))
	for _, n := range names {
		out = append(out, *byName[n])
	}
	return out, nil
}

// SetCredential stores one of the server's credentials; "" removes it.
func (s *Store) SetCredential(ctx context.Context, name, value string) error {
	if value == "" {
		return s.DeleteSecret(ctx, CredentialScope, name)
	}
	return s.put(ctx, CredentialScope, name, value)
}

func (s *Store) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Store) seal(plain []byte) ([]byte, error) {
	g, err := s.gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	rand.Read(nonce)
	return g.Seal(nonce, nonce, plain, nil), nil
}

func (s *Store) open(sealed []byte) ([]byte, error) {
	g, err := s.gcm()
	if err != nil {
		return nil, err
	}
	if len(sealed) < g.NonceSize() {
		return nil, errors.New("store: corrupt secret")
	}
	return g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], nil)
}
