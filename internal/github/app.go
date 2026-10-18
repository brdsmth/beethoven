package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/brdsmth/beethoven/internal/store"
)

// App is a GitHub App's credentials, in the form GitHub's app manifest
// conversion returns them.
type App struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name,omitempty"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
	PEM           string `json:"pem"` // the private key
	HTMLURL       string `json:"html_url,omitempty"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`

	key *rsa.PrivateKey
}

// ParseApp reads an app's credentials from JSON.
func ParseApp(b []byte) (*App, error) {
	var a App
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("github app: %w", err)
	}
	if a.ID == 0 || a.Slug == "" || a.ClientID == "" || a.ClientSecret == "" || a.PEM == "" {
		return nil, errors.New("github app: id, slug, client_id, client_secret and pem are required")
	}
	block, _ := pem.Decode([]byte(a.PEM))
	if block == nil {
		return nil, errors.New("github app: pem holds no private key")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		a.key = k
		return &a, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github app: private key: %w", err)
	}
	var ok bool
	if a.key, ok = k.(*rsa.PrivateKey); !ok {
		return nil, errors.New("github app: the private key is not an RSA key")
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

// App returns the GitHub App the server acts as and where it comes from,
// "environment" or "settings"; nil if there is none.
func (c *Client) App(ctx context.Context) (*App, string) {
	if c.EnvApp != nil {
		return c.EnvApp, "environment"
	}
	if c.DB == nil {
		return nil, ""
	}
	creds, err := c.DB.Credentials(ctx)
	raw := creds[store.GitHubAppCredential]
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

func (a *App) raw() string {
	b, _ := json.Marshal(a)
	return string(b)
}

// InstallURL is where a person installs the app on their repositories.
func (c *Client) InstallURL(app *App) string {
	u := c.web() + "/apps/" + app.Slug + "/installations/new"
	if c.InstallState != "" {
		u += "?state=" + url.QueryEscape(c.InstallState)
	}
	return u
}

// Manifest describes the GitHub App a server registers for itself. base is
// the server's URL as the browser reaches it: GitHub returns the browser
// there after each step. Any account can install a public app; a private
// one only the account that owns it.
func Manifest(base string, public bool) map[string]any {
	suffix := make([]byte, 3)
	rand.Read(suffix)
	return map[string]any{
		"name":            "Beethoven " + hex.EncodeToString(suffix), // app names are unique across GitHub
		"url":             "https://github.com/brdsmth/beethoven",
		"description":     "Background coding agents: clones repositories, pushes work branches and opens pull requests.",
		"redirect_url":    base + "/github/created",
		"callback_urls":   []string{base + "/github/authorized"},
		"setup_url":       base + "/github/installed",
		"setup_on_update": true,
		"public":          public,
		"default_permissions": map[string]string{
			"contents":      "write", // clone and push
			"pull_requests": "write",
			"workflows":     "write", // push changes to .github/workflows
			"metadata":      "read",
			// Start new projects. Unlike administration, it allows nothing
			// else, and the app gets access to the repositories it creates.
			"repository_creation": "write",
		},
	}
}

// ManifestURL is where the browser posts the manifest: to register the app
// under the person's own account, or under org.
func (c *Client) ManifestURL(org, state string) string {
	u := c.web() + "/settings/apps/new"
	if org != "" {
		u = c.web() + "/organizations/" + url.PathEscape(org) + "/settings/apps/new"
	}
	return u + "?state=" + url.QueryEscape(state)
}

// RegisterApp completes the manifest flow: it trades the code GitHub sent
// back for the new app's credentials and stores them.
func (c *Client) RegisterApp(ctx context.Context, code string) (*App, error) {
	var raw json.RawMessage
	if err := c.call(ctx, "", "POST", "/app-manifests/"+url.PathEscape(code)+"/conversions", nil, &raw); err != nil {
		return nil, err
	}
	app, err := ParseApp(raw)
	if err != nil {
		return nil, err
	}
	return app, c.DB.SetCredential(ctx, store.GitHubAppCredential, app.raw())
}

// ForgetApp removes an app registered here, and its accounts. The app
// itself remains on GitHub.
func (c *Client) ForgetApp(ctx context.Context) error {
	app, source := c.App(ctx)
	if source != "settings" {
		return fmt.Errorf("%w: only an app registered from this server can be removed here", store.ErrInvalid)
	}
	if err := c.DB.DeleteGitHubAccounts(ctx, app.ID, 0); err != nil {
		return err
	}
	return c.DB.SetCredential(ctx, store.GitHubAppCredential, "")
}

// jwt authenticates as the app itself, for ten minutes.
func (a *App) jwt() (string, error) {
	enc := base64.RawURLEncoding
	iss := a.ClientID
	if iss == "" {
		iss = strconv.FormatInt(a.ID, 10)
	}
	now := time.Now()
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": iss})
	signed := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signed + "." + enc.EncodeToString(sig), nil
}

type minted struct {
	token   string
	expires time.Time
}

// repoToken returns an installation token for one repository. The app's
// installation on it must be one the connected person can access. A public
// repository the app is not installed on is cloned without a token.
func (c *Client) repoToken(ctx context.Context, cn *conn, repo string) (string, error) {
	key := fmt.Sprintf("%d:%d:%s", cn.app.ID, cn.acct.ID, strings.ToLower(repo))
	c.mu.Lock()
	m, ok := c.minted[key]
	c.mu.Unlock()
	if ok && time.Until(m.expires) > 10*time.Minute {
		return m.token, nil
	}
	id, err := c.installation(ctx, cn, repo)
	if err != nil || id == 0 {
		return "", err
	}
	jwt, err := cn.app.jwt()
	if err != nil {
		return "", err
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	_, name, _ := strings.Cut(repo, "/")
	path := fmt.Sprintf("/app/installations/%d/access_tokens", id)
	if err := c.call(ctx, jwt, "POST", path, map[string]any{"repositories": []string{name}}, &out); err != nil {
		return "", err
	}
	c.mu.Lock()
	if c.minted == nil {
		c.minted = map[string]minted{}
	}
	c.minted[key] = minted{token: out.Token, expires: out.ExpiresAt}
	c.mu.Unlock()
	return out.Token, nil
}

// installation finds the app's installation on repo and checks that the
// connected person can access it. It returns 0 for a public repository the
// app is not installed on.
func (c *Client) installation(ctx context.Context, cn *conn, repo string) (int64, error) {
	jwt, err := cn.app.jwt()
	if err != nil {
		return 0, err
	}
	var inst struct {
		ID int64 `json:"id"`
	}
	err = c.call(ctx, jwt, "GET", "/repos/"+repo+"/installation", nil, &inst)
	if isNotFound(err) {
		var r struct {
			Private bool `json:"private"`
		}
		if tok, terr := c.userToken(ctx, cn); terr == nil && c.call(ctx, tok, "GET", "/repos/"+repo, nil, &r) == nil && !r.Private {
			return 0, nil
		}
		return 0, fmt.Errorf("the GitHub App is not installed on %s; add the repository at %s", repo, c.InstallURL(cn.app))
	}
	if err != nil {
		return 0, err
	}
	insts, err := c.installations(ctx, cn)
	if err != nil {
		return 0, err
	}
	for _, in := range insts {
		if in.ID == inst.ID {
			return inst.ID, nil
		}
	}
	return 0, fmt.Errorf("@%s has no access to the GitHub App's installation on %s", cn.acct.Login, repo)
}

// Installation is where the app is installed: an account or organization,
// on all of its repositories or selected ones.
type Installation struct {
	ID        int64  `json:"id"`
	Account   string `json:"account"`
	Type      string `json:"type"`      // User or Organization
	Selection string `json:"selection"` // all or selected
	URL       string `json:"url"`       // where it is managed

	permissions map[string]string // what the account granted the app
}

// installations lists the app's installations the connected person can access.
func (c *Client) installations(ctx context.Context, cn *conn) ([]Installation, error) {
	tok, err := c.userToken(ctx, cn)
	if err != nil {
		return nil, err
	}
	var out struct {
		Installations []struct {
			ID      int64 `json:"id"`
			Account struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"account"`
			Selection   string            `json:"repository_selection"`
			HTMLURL     string            `json:"html_url"`
			Permissions map[string]string `json:"permissions"`
		} `json:"installations"`
	}
	if err := c.call(ctx, tok, "GET", "/user/installations?per_page=100", nil, &out); err != nil {
		return nil, err
	}
	list := make([]Installation, 0, len(out.Installations))
	for _, in := range out.Installations {
		list = append(list, Installation{ID: in.ID, Account: in.Account.Login, Type: in.Account.Type, Selection: in.Selection,
			URL: in.HTMLURL, permissions: in.Permissions})
	}
	return list, nil
}
