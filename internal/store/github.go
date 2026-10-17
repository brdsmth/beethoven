package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// GitHubAppCredential names the credential holding a GitHub App registered
// from this server: GitHub's app manifest conversion, as JSON.
const GitHubAppCredential = "GITHUB_APP"

// GitHubAccount is a GitHub user who authorized the server's GitHub App:
// the GitHub identity of one person here.
type GitHubAccount struct {
	AppID       int64
	ID          int64  // GitHub user id
	AccountID   string // the person here who connected it; "" before there were people
	Login       string
	Name        string
	Tokens      GitHubTokens
	ConnectedAt time.Time
	UpdatedAt   time.Time
}

// GitHubTokens are a user's tokens for a GitHub App. Zero expiry times mean
// the token does not expire.
type GitHubTokens struct {
	Access         string    `json:"access"`
	AccessExpires  time.Time `json:"access_expires"`
	Refresh        string    `json:"refresh,omitempty"`
	RefreshExpires time.Time `json:"refresh_expires"`
}

// GitHubAccount returns the most recently connected account of an app.
func (s *Store) GitHubAccount(ctx context.Context, appID int64) (GitHubAccount, error) {
	return s.githubAccount(ctx, `WHERE app_id=? ORDER BY connected_at DESC LIMIT 1`, appID)
}

// GitHubAccountOf returns the account a person here connected to an app.
func (s *Store) GitHubAccountOf(ctx context.Context, appID int64, accountID string) (GitHubAccount, error) {
	return s.githubAccount(ctx, `WHERE app_id=? AND account_id=? ORDER BY connected_at DESC LIMIT 1`, appID, accountID)
}

// GitHubAccountByID returns an app's account by GitHub user id.
func (s *Store) GitHubAccountByID(ctx context.Context, appID, id int64) (GitHubAccount, error) {
	return s.githubAccount(ctx, `WHERE app_id=? AND id=?`, appID, id)
}

// GitHubConnections maps the people here onto the GitHub logins they
// connected to an app.
func (s *Store) GitHubConnections(ctx context.Context, appID int64) (map[string]string, error) {
	out := map[string]string{}
	_, err := query(ctx, s.r, func(r scanner) (struct{}, error) {
		var account, login string
		err := r.Scan(&account, &login)
		out[account] = login
		return struct{}{}, err
	}, `SELECT account_id, login FROM github_accounts WHERE app_id=? ORDER BY connected_at`, appID)
	return out, err
}

func (s *Store) githubAccount(ctx context.Context, where string, args ...any) (GitHubAccount, error) {
	var a GitHubAccount
	var sealed []byte
	var connected, updated int64
	err := s.r.QueryRowContext(ctx, `SELECT app_id, id, account_id, login, name, tokens, connected_at, updated_at
		FROM github_accounts `+where, args...).
		Scan(&a.AppID, &a.ID, &a.AccountID, &a.Login, &a.Name, &sealed, &connected, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	plain, err := s.open(sealed)
	if err != nil {
		return a, err
	}
	a.ConnectedAt, a.UpdatedAt = fromMS(connected), fromMS(updated)
	return a, json.Unmarshal(plain, &a.Tokens)
}

// SaveGitHubAccount creates or replaces an account. A GitHub user belongs
// to one person here: connecting it again moves it to whoever did.
func (s *Store) SaveGitHubAccount(ctx context.Context, a *GitHubAccount) error {
	plain, _ := json.Marshal(a.Tokens)
	sealed, err := s.seal(plain)
	if err != nil {
		return err
	}
	a.UpdatedAt = time.Now()
	if a.ConnectedAt.IsZero() {
		a.ConnectedAt = a.UpdatedAt
	}
	_, err = s.w.ExecContext(ctx, `INSERT INTO github_accounts (app_id, id, account_id, login, name, tokens, connected_at,
		updated_at) VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(app_id, id) DO UPDATE SET account_id=excluded.account_id,
		login=excluded.login, name=excluded.name, tokens=excluded.tokens, connected_at=excluded.connected_at,
		updated_at=excluded.updated_at`,
		a.AppID, a.ID, a.AccountID, a.Login, a.Name, sealed, ms(a.ConnectedAt), ms(a.UpdatedAt))
	return err
}

// DeleteGitHubAccounts forgets an app's accounts: one, or all when id is 0.
func (s *Store) DeleteGitHubAccounts(ctx context.Context, appID, id int64) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM github_accounts WHERE app_id=? AND (?=0 OR id=?)`, appID, id, id)
	return err
}
