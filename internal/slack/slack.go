// Package slack lets people start and continue sessions from Slack.
//
// The server talks to Slack as a Slack app: a preconfigured one, shared by
// every workspace an operator serves, or one created from the server itself
// with Slack's manifest API. Installing the app in a workspace ("Add to
// Slack") gives the server a bot token for it. Slack delivers events and
// button clicks over HTTPS, so the server needs a public HTTPS address.
//
// Client covers the app and its installations; Bot is what people talk to.
package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/brdsmth/beethoven/internal/store"
)

// App is a Slack app's credentials.
type App struct {
	AppID         string `json:"app_id"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	SigningSecret string `json:"signing_secret"`
}

// ParseApp reads an app's credentials from JSON.
func ParseApp(b []byte) (*App, error) {
	var a App
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("slack app: %w", err)
	}
	if a.AppID == "" || a.ClientID == "" || a.ClientSecret == "" || a.SigningSecret == "" {
		return nil, errors.New("slack app: app_id, client_id, client_secret and signing_secret are required")
	}
	return &a, nil
}

// LoadApp reads a preconfigured app: its JSON, or the path of a file
// holding it.
func LoadApp(v string) (*App, error) {
	b := []byte(v)
	if !strings.HasPrefix(strings.TrimSpace(v), "{") {
		var err error
		if b, err = os.ReadFile(v); err != nil {
			return nil, err
		}
	}
	return ParseApp(b)
}

// Scopes are what the bot asks for in a workspace.
var Scopes = []string{
	"app_mentions:read", "im:history", // requests: mentions and direct messages
	"chat:write", "reactions:write", // answers
	"users:read", "users:read.email", // who asks: members only, and the commit author
	"channels:history", "groups:history", "mpim:history", // the thread a request is part of
	"channels:read", "groups:read", // channel names, for the settings
	"files:read", // files shared with requests
}

type Client struct {
	API    string // default https://slack.com/api
	Web    string // default https://slack.com
	HTTP   *http.Client
	DB     *store.Store // the app created here and the installations
	EnvApp *App         // a preconfigured app; takes precedence over one created here

	mu        sync.Mutex
	parsed    *App
	parsedRaw string
}

// App returns the Slack app the server acts as and where it comes from,
// "environment" or "settings"; nil if there is none.
func (c *Client) App(ctx context.Context) (*App, string) {
	if c.EnvApp != nil {
		return c.EnvApp, "environment"
	}
	creds, err := c.DB.Credentials(ctx)
	raw := creds[store.SlackAppCredential]
	if err != nil || raw == "" {
		return nil, ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.parsedRaw != raw {
		app, err := ParseApp([]byte(raw))
		if err != nil {
			return nil, ""
		}
		c.parsed, c.parsedRaw = app, raw
	}
	return c.parsed, "settings"
}

// Manifest describes the Slack app a server creates for itself. base is
// the server's public HTTPS URL: Slack sends events and clicks there.
func Manifest(base string) map[string]any {
	return map[string]any{
		"display_information": map[string]any{
			"name":             "Beethoven",
			"description":      "Background coding agents: mention it with a task and it opens a pull request.",
			"background_color": "#1d1f24",
		},
		"features": map[string]any{
			"app_home": map[string]any{"home_tab_enabled": false, "messages_tab_enabled": true, "messages_tab_read_only_enabled": false},
			"bot_user": map[string]any{"display_name": "beethoven", "always_online": true},
		},
		"oauth_config": map[string]any{
			"redirect_urls": []string{base + "/slack/installed"},
			"scopes":        map[string]any{"bot": Scopes},
		},
		"settings": map[string]any{
			"event_subscriptions":    map[string]any{"request_url": base + "/slack/events", "bot_events": []string{"app_mention", "message.im"}},
			"interactivity":          map[string]any{"is_enabled": true, "request_url": base + "/slack/interactions"},
			"org_deploy_enabled":     false,
			"socket_mode_enabled":    false,
			"token_rotation_enabled": false,
		},
	}
}

// CreateApp creates the server's app from its manifest with a person's app
// configuration token, used once and not kept, and stores the app.
func (c *Client) CreateApp(ctx context.Context, configToken, base string) (*App, error) {
	manifest, _ := json.Marshal(Manifest(base))
	var out struct {
		AppID       string `json:"app_id"`
		Credentials struct {
			ClientID      string `json:"client_id"`
			ClientSecret  string `json:"client_secret"`
			SigningSecret string `json:"signing_secret"`
		} `json:"credentials"`
	}
	if err := c.call(ctx, configToken, "apps.manifest.create", url.Values{"manifest": {string(manifest)}}, &out); err != nil {
		return nil, err
	}
	app := &App{AppID: out.AppID, ClientID: out.Credentials.ClientID, ClientSecret: out.Credentials.ClientSecret,
		SigningSecret: out.Credentials.SigningSecret}
	raw, _ := json.Marshal(app)
	if _, err := ParseApp(raw); err != nil {
		return nil, err
	}
	return app, c.DB.SetCredential(ctx, store.SlackAppCredential, string(raw))
}

// ForgetApp removes an app created here, and its installations. The app
// itself remains on Slack.
func (c *Client) ForgetApp(ctx context.Context) error {
	app, source := c.App(ctx)
	if source != "settings" {
		return fmt.Errorf("%w: only an app created from this server can be removed here", store.ErrInvalid)
	}
	if err := c.DB.DeleteSlackInstallations(ctx, app.AppID, ""); err != nil {
		return err
	}
	return c.DB.SetCredential(ctx, store.SlackAppCredential, "")
}

// InstallURL sends a person to Slack to add the app to a workspace. Slack
// returns them to redirect with a code and the state.
func (c *Client) InstallURL(app *App, redirect, state string) string {
	q := url.Values{"client_id": {app.ClientID}, "scope": {strings.Join(Scopes, ",")}, "redirect_uri": {redirect}, "state": {state}}
	return c.web() + "/oauth/v2/authorize?" + q.Encode()
}

// Install completes an installation: it trades the code for the
// workspace's bot token and stores it. base is the server's URL, for links
// back from Slack.
func (c *Client) Install(ctx context.Context, code, redirect, base string) (store.SlackInstallation, error) {
	app, _ := c.App(ctx)
	if app == nil {
		return store.SlackInstallation{}, fmt.Errorf("%w: no Slack app is configured", store.ErrInvalid)
	}
	form := url.Values{"client_id": {app.ClientID}, "client_secret": {app.ClientSecret}, "code": {code}, "redirect_uri": {redirect}}
	var out struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		BotUserID   string `json:"bot_user_id"`
		Team        struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
		AuthedUser struct {
			ID string `json:"id"`
		} `json:"authed_user"`
	}
	if err := c.call(ctx, "", "oauth.v2.access", form, &out); err != nil {
		return store.SlackInstallation{}, err
	}
	if out.TokenType != "bot" || out.AccessToken == "" || out.Team.ID == "" {
		return store.SlackInstallation{}, errors.New("slack: the installation returned no bot token")
	}
	in := store.SlackInstallation{TeamID: out.Team.ID, AppID: app.AppID, TeamName: out.Team.Name, BotUserID: out.BotUserID,
		BotToken: out.AccessToken, Installer: out.AuthedUser.ID, BaseURL: base}
	return in, c.DB.SaveSlackInstallation(ctx, &in)
}

// Uninstall revokes the app's token in a workspace, as far as Slack can be
// reached, and forgets the installation.
func (c *Client) Uninstall(ctx context.Context, teamID string) error {
	app, _ := c.App(ctx)
	if app == nil {
		return store.ErrNotFound
	}
	in, err := c.DB.SlackInstallation(ctx, app.AppID, teamID)
	if err != nil {
		return err
	}
	c.call(ctx, in.BotToken, "auth.revoke", nil, nil)
	return c.DB.DeleteSlackInstallations(ctx, app.AppID, teamID)
}

// Installation returns the app's installation in a workspace.
func (c *Client) Installation(ctx context.Context, teamID string) (*App, store.SlackInstallation, error) {
	app, _ := c.App(ctx)
	if app == nil {
		return nil, store.SlackInstallation{}, store.ErrNotFound
	}
	in, err := c.DB.SlackInstallation(ctx, app.AppID, teamID)
	return app, in, err
}

// Status describes the app, where it is installed, and channel defaults.
type Status struct {
	App           *AppStatus                `json:"app"` // nil without an app
	Installations []store.SlackInstallation `json:"installations"`
	Channels      []store.SlackChannel      `json:"channels"`
}

type AppStatus struct {
	AppID       string `json:"app_id"`
	Source      string `json:"source"` // environment (preconfigured) or settings (created here)
	SettingsURL string `json:"settings_url"`
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	st := Status{Installations: []store.SlackInstallation{}, Channels: []store.SlackChannel{}}
	app, source := c.App(ctx)
	if app == nil {
		return st, nil
	}
	st.App = &AppStatus{AppID: app.AppID, Source: source, SettingsURL: "https://api.slack.com/apps/" + app.AppID}
	var err error
	if st.Installations, err = c.DB.SlackInstallations(ctx, app.AppID); err != nil {
		return st, err
	}
	if st.Channels, err = c.DB.SlackChannels(ctx); err != nil {
		return st, err
	}
	if st.Installations == nil {
		st.Installations = []store.SlackInstallation{}
	}
	if st.Channels == nil {
		st.Channels = []store.SlackChannel{}
	}
	return st, nil
}

// apiError is a Web API reply with ok: false.
type apiError struct {
	Method, Code string
	Details      []string
}

func (e *apiError) Error() string {
	msg := "slack " + e.Method + ": " + e.Code
	if len(e.Details) > 0 {
		msg += ": " + strings.Join(e.Details, "; ")
	}
	return msg
}

// errorCode returns the Web API error code of err, if it is one.
func errorCode(err error) string {
	var e *apiError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// call invokes a Web API method with form-encoded arguments, authorized
// with token if one is given.
func (c *Client) call(ctx context.Context, token, method string, args url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, "POST", c.api()+"/"+method, strings.NewReader(args.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var reply struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Errors []struct {
			Message string `json:"message"`
			Pointer string `json:"pointer"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return fmt.Errorf("slack %s: %s", method, resp.Status)
	}
	if !reply.OK {
		e := &apiError{Method: method, Code: reply.Error}
		for _, x := range reply.Errors {
			e.Details = append(e.Details, strings.TrimSpace(x.Pointer+" "+x.Message))
		}
		return e
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) api() string {
	if c.API == "" {
		return "https://slack.com/api"
	}
	return c.API
}

func (c *Client) web() string {
	if c.Web == "" {
		return "https://slack.com"
	}
	return c.Web
}

func (c *Client) http() *http.Client {
	if c.HTTP == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return c.HTTP
}
