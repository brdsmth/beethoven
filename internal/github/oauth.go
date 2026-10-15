package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/brdsmth/beethoven/internal/store"
)

// AuthorizeURL sends a person to GitHub to authorize the app. GitHub
// returns them to redirect with a code and the state.
func (c *Client) AuthorizeURL(app *App, redirect, state string) string {
	q := url.Values{"client_id": {app.ClientID}, "redirect_uri": {redirect}, "state": {state}}
	return c.web() + "/login/oauth/authorize?" + q.Encode()
}

// Connect completes an authorization: it trades the code for the person's
// tokens and makes them the account the server acts for.
func (c *Client) Connect(ctx context.Context, code, redirect string) (store.GitHubAccount, error) {
	app, _ := c.App(ctx)
	if app == nil {
		return store.GitHubAccount{}, fmt.Errorf("%w: no GitHub App is configured", store.ErrInvalid)
	}
	tokens, err := c.exchange(ctx, app, url.Values{"code": {code}, "redirect_uri": {redirect}})
	if err != nil {
		return store.GitHubAccount{}, err
	}
	var u struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := c.call(ctx, tokens.Access, "GET", "/user", nil, &u); err != nil {
		return store.GitHubAccount{}, err
	}
	acct := store.GitHubAccount{AppID: app.ID, ID: u.ID, Login: u.Login, Name: u.Name, Tokens: tokens}
	return acct, c.DB.SaveGitHubAccount(ctx, &acct)
}

// Disconnect forgets the connected account and revokes its authorization
// on GitHub, as far as GitHub can be reached.
func (c *Client) Disconnect(ctx context.Context) error {
	cn, err := c.connection(ctx)
	if err != nil || cn == nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"access_token": cn.acct.Tokens.Access})
	req, err := http.NewRequestWithContext(ctx, "DELETE", c.api()+"/applications/"+cn.app.ClientID+"/grant", bytes.NewReader(body))
	if err == nil {
		req.SetBasicAuth(cn.app.ClientID, cn.app.ClientSecret)
		req.Header.Set("Accept", "application/vnd.github+json")
		if resp, err := c.http().Do(req); err == nil {
			resp.Body.Close()
		}
	}
	return c.DB.DeleteGitHubAccounts(ctx, cn.app.ID, cn.acct.ID)
}

// userToken returns the connected person's access token, refreshed when it
// is about to expire.
func (c *Client) userToken(ctx context.Context, cn *conn) (string, error) {
	if !expiring(cn.acct.Tokens) {
		return cn.acct.Tokens.Access, nil
	}
	c.refresh.Lock()
	defer c.refresh.Unlock()
	acct, err := c.DB.GitHubAccount(ctx, cn.app.ID) // another call may have refreshed it meanwhile
	if err != nil {
		return "", err
	}
	if cn.acct = acct; !expiring(acct.Tokens) {
		return acct.Tokens.Access, nil
	}
	reconnect := fmt.Errorf("GitHub authorization for @%s has expired; connect GitHub again", acct.Login)
	if acct.Tokens.Refresh == "" || (!acct.Tokens.RefreshExpires.IsZero() && time.Now().After(acct.Tokens.RefreshExpires)) {
		return "", reconnect
	}
	tokens, err := c.exchange(ctx, cn.app, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {acct.Tokens.Refresh}})
	if err != nil {
		return "", fmt.Errorf("%w (%v)", reconnect, err)
	}
	acct.Tokens = tokens
	if err := c.DB.SaveGitHubAccount(ctx, &acct); err != nil {
		return "", err
	}
	cn.acct = acct
	return tokens.Access, nil
}

func expiring(t store.GitHubTokens) bool {
	return !t.AccessExpires.IsZero() && time.Until(t.AccessExpires) < 5*time.Minute
}

// exchange calls GitHub's token endpoint, with the app's client
// credentials, for a new or refreshed user token.
func (c *Client) exchange(ctx context.Context, app *App, form url.Values) (store.GitHubTokens, error) {
	form.Set("client_id", app.ClientID)
	form.Set("client_secret", app.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, "POST", c.web()+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return store.GitHubTokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return store.GitHubTokens{}, err
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken      string `json:"access_token"`
		ExpiresIn        int    `json:"expires_in"`
		RefreshToken     string `json:"refresh_token"`
		RefreshExpiresIn int    `json:"refresh_token_expires_in"`
		Error            string `json:"error"`
		Description      string `json:"error_description"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(data, &out); err != nil || resp.StatusCode >= 300 {
		return store.GitHubTokens{}, fmt.Errorf("github token exchange: %s", resp.Status)
	}
	if out.Error != "" { // reported with 200 OK
		return store.GitHubTokens{}, fmt.Errorf("github token exchange: %s: %s", out.Error, out.Description)
	}
	if out.AccessToken == "" {
		return store.GitHubTokens{}, errors.New("github token exchange: no token in the response")
	}
	now := time.Now()
	t := store.GitHubTokens{Access: out.AccessToken, Refresh: out.RefreshToken}
	if out.ExpiresIn > 0 {
		t.AccessExpires = now.Add(time.Duration(out.ExpiresIn) * time.Second)
	}
	if out.RefreshExpiresIn > 0 {
		t.RefreshExpires = now.Add(time.Duration(out.RefreshExpiresIn) * time.Second)
	}
	return t, nil
}

// Status describes how the server reaches GitHub.
type Status struct {
	App           *AppStatus     `json:"app"`     // nil without an app
	Account       *AccountStatus `json:"account"` // nil until someone connects
	Installations []Installation `json:"installations"`
	Token         string         `json:"token"` // where a personal access token comes from; used until someone connects
	Error         string         `json:"error,omitempty"`
}

type AppStatus struct {
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	Owner      string `json:"owner"`
	URL        string `json:"url"`
	InstallURL string `json:"install_url"`
	Source     string `json:"source"` // environment (preconfigured) or settings (registered here)
}

type AccountStatus struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
}

func (c *Client) Status(ctx context.Context) Status {
	st := Status{Installations: []Installation{}, Token: c.TokenSource(ctx)}
	app, source := c.App(ctx)
	if app == nil {
		return st
	}
	st.App = &AppStatus{Name: app.Name, Slug: app.Slug, Owner: app.Owner.Login, URL: c.web() + "/apps/" + app.Slug,
		InstallURL: c.InstallURL(app), Source: source}
	cn, err := c.connection(ctx)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	if cn == nil {
		return st
	}
	st.Account = &AccountStatus{ID: cn.acct.ID, Login: cn.acct.Login, Name: cn.acct.Name}
	if insts, err := c.installations(ctx, cn); err != nil {
		st.Error = err.Error()
	} else {
		st.Installations = insts
	}
	return st
}
