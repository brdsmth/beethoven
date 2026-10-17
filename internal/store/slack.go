package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SlackAppCredential names the credential holding a Slack app created from
// this server, as JSON.
const SlackAppCredential = "SLACK_APP"

// SlackInstallation is the server's Slack app installed in a workspace.
type SlackInstallation struct {
	TeamID    string    `json:"team_id"`
	AppID     string    `json:"app_id"`
	TeamName  string    `json:"team_name"`
	BotUserID string    `json:"bot_user_id"`
	BotToken  string    `json:"-"`
	Installer string    `json:"installer"`
	BaseURL   string    `json:"base_url"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) SaveSlackInstallation(ctx context.Context, in *SlackInstallation) error {
	sealed, err := s.seal([]byte(in.BotToken))
	if err != nil {
		return err
	}
	now := time.Now()
	if in.CreatedAt.IsZero() {
		in.CreatedAt = now
	}
	_, err = s.w.ExecContext(ctx, `INSERT INTO slack_installations (team_id, app_id, team_name, bot_user_id, bot_token,
		installer, base_url, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(team_id) DO UPDATE SET app_id=excluded.app_id, team_name=excluded.team_name,
		bot_user_id=excluded.bot_user_id, bot_token=excluded.bot_token, installer=excluded.installer,
		base_url=excluded.base_url, updated_at=excluded.updated_at`,
		in.TeamID, in.AppID, in.TeamName, in.BotUserID, sealed, in.Installer, in.BaseURL, ms(in.CreatedAt), ms(now))
	return err
}

const slackInstallCols = `team_id, app_id, team_name, bot_user_id, bot_token, installer, base_url, created_at`

func (s *Store) scanSlackInstallation(row scanner) (SlackInstallation, error) {
	var in SlackInstallation
	var sealed []byte
	var created int64
	err := row.Scan(&in.TeamID, &in.AppID, &in.TeamName, &in.BotUserID, &sealed, &in.Installer, &in.BaseURL, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return in, ErrNotFound
	}
	if err != nil {
		return in, err
	}
	tok, err := s.open(sealed)
	in.BotToken, in.CreatedAt = string(tok), fromMS(created)
	return in, err
}

// SlackInstallation returns an app's installation in a workspace.
func (s *Store) SlackInstallation(ctx context.Context, appID, teamID string) (SlackInstallation, error) {
	return s.scanSlackInstallation(s.r.QueryRowContext(ctx,
		`SELECT `+slackInstallCols+` FROM slack_installations WHERE app_id=? AND team_id=?`, appID, teamID))
}

// SlackInstallations lists an app's installations.
func (s *Store) SlackInstallations(ctx context.Context, appID string) ([]SlackInstallation, error) {
	return query(ctx, s.r, s.scanSlackInstallation,
		`SELECT `+slackInstallCols+` FROM slack_installations WHERE app_id=? ORDER BY team_name`, appID)
}

// DeleteSlackInstallations forgets installations: one workspace's, or all
// of an app's when teamID is "".
func (s *Store) DeleteSlackInstallations(ctx context.Context, appID, teamID string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM slack_installations WHERE app_id=? AND (?='' OR team_id=?)`, appID, teamID, teamID)
	return err
}

// SlackRequest is a request made in Slack, keyed by the dedup key of the
// prompt it became.
type SlackRequest struct {
	Key       string
	TeamID    string
	Channel   string
	ThreadTS  string
	MessageTS string
	StatusTS  string
}

// AddSlackRequest records a request, unless one with its key exists:
// added reports which.
func (s *Store) AddSlackRequest(ctx context.Context, r SlackRequest) (added bool, err error) {
	res, err := s.w.ExecContext(ctx, `INSERT INTO slack_requests (key, team_id, channel, thread_ts, message_ts, status_ts, created_at)
		VALUES (?,?,?,?,?,?,?) ON CONFLICT(key) DO NOTHING`,
		r.Key, r.TeamID, r.Channel, r.ThreadTS, r.MessageTS, r.StatusTS, ms(time.Now()))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *Store) SetSlackStatus(ctx context.Context, key, statusTS string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE slack_requests SET status_ts=? WHERE key=?`, statusTS, key)
	return err
}

func (s *Store) SlackRequest(ctx context.Context, key string) (SlackRequest, error) {
	var r SlackRequest
	err := s.r.QueryRowContext(ctx, `SELECT key, team_id, channel, thread_ts, message_ts, status_ts FROM slack_requests WHERE key=?`,
		key).Scan(&r.Key, &r.TeamID, &r.Channel, &r.ThreadTS, &r.MessageTS, &r.StatusTS)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// LastSlackRequest returns the timestamp of the latest request made in a
// thread before the message at ts, or "" if there is none.
func (s *Store) LastSlackRequest(ctx context.Context, teamID, channel, threadTS, ts string) (string, error) {
	var last sql.NullString
	err := s.r.QueryRowContext(ctx, `SELECT MAX(message_ts) FROM slack_requests
		WHERE team_id=? AND channel=? AND thread_ts=? AND message_ts < ?`, teamID, channel, threadTS, ts).Scan(&last)
	return last.String, err
}

// SlackChannel is what a channel works on when a request names nothing: an
// environment, repositories, or (with neither) a scratch workspace; and the
// team its requests belong to.
type SlackChannel struct {
	TeamID        string    `json:"team_id"` // the Slack workspace
	Channel       string    `json:"channel"`
	OwnerTeamID   string    `json:"owner_team_id,omitempty"` // the Beethoven team; "" = the workspace
	Name          string    `json:"name"`
	EnvironmentID string    `json:"environment_id,omitempty"`
	Repos         []RepoRef `json:"repos"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (s *Store) SaveSlackChannel(ctx context.Context, c *SlackChannel) error {
	c.UpdatedAt = time.Now()
	_, err := s.w.ExecContext(ctx, `INSERT INTO slack_channels (team_id, channel, name, environment_id, repos,
		owner_team_id, updated_at) VALUES (?,?,?,?,?,?,?) ON CONFLICT(team_id, channel) DO UPDATE SET name=excluded.name,
		environment_id=excluded.environment_id, repos=excluded.repos, owner_team_id=excluded.owner_team_id,
		updated_at=excluded.updated_at`,
		c.TeamID, c.Channel, c.Name, c.EnvironmentID, encodeRepos(c.Repos), c.OwnerTeamID, ms(c.UpdatedAt))
	return err
}

func scanSlackChannel(row scanner) (SlackChannel, error) {
	var c SlackChannel
	var repos string
	var updated int64
	err := row.Scan(&c.TeamID, &c.Channel, &c.Name, &c.EnvironmentID, &repos, &c.OwnerTeamID, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	c.Repos, c.UpdatedAt = decodeRepos(repos), fromMS(updated)
	return c, err
}

const slackChannelCols = `team_id, channel, name, environment_id, repos, owner_team_id, updated_at`

func (s *Store) SlackChannel(ctx context.Context, teamID, channel string) (SlackChannel, error) {
	return scanSlackChannel(s.r.QueryRowContext(ctx,
		`SELECT `+slackChannelCols+` FROM slack_channels WHERE team_id=? AND channel=?`, teamID, channel))
}

func (s *Store) SlackChannels(ctx context.Context) ([]SlackChannel, error) {
	return query(ctx, s.r, scanSlackChannel, `SELECT `+slackChannelCols+` FROM slack_channels ORDER BY name`)
}

func (s *Store) DeleteSlackChannel(ctx context.Context, teamID, channel string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM slack_channels WHERE team_id=? AND channel=?`, teamID, channel)
	return err
}
