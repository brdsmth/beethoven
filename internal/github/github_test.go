package github_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/github"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

var ctx = context.Background()

func client(t *testing.T, gh *fake.GitHub) *github.Client {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := github.New("pat")
	c.API, c.Web, c.DB = gh.URL, gh.URL, db
	return c
}

// connected registers the app from a manifest and connects its user.
func connected(t *testing.T, gh *fake.GitHub) *github.Client {
	t.Helper()
	c := client(t, gh)
	if _, err := c.RegisterApp(ctx, "manifest-code"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Connect(ctx, "oauth-code", "http://beethoven.test/github/authorized"); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPersonalTokenUntilConnected(t *testing.T) {
	gh := fake.NewGitHub(t)
	c := client(t, gh)
	if tok, _ := c.Token(ctx, "acme/app"); tok != "pat" || c.Mode(ctx) != "token" {
		t.Fatalf("token %q, mode %q; want the personal access token", tok, c.Mode(ctx))
	}
	if _, err := c.RegisterApp(ctx, "manifest-code"); err != nil {
		t.Fatal(err)
	}
	if app, source := c.App(ctx); app == nil || app.Slug != "beethoven-test" || source != "settings" {
		t.Fatalf("app = %+v from %q", app, source)
	}
	if c.Mode(ctx) != "token" { // registered, but nobody has connected yet
		t.Errorf("mode = %q", c.Mode(ctx))
	}
}

func TestConnectedAppMintsTokensPerRepository(t *testing.T) {
	gh := fake.NewGitHub(t)
	gh.Install("acme/app", 1, true)
	gh.Install("acme/lib", 1, true)
	gh.Install("other/secret", 2, false) // an installation the user can't access
	gh.SetPublic("torvalds/linux")
	c := connected(t, gh)

	if c.Mode(ctx) != "app" {
		t.Fatalf("mode = %q", c.Mode(ctx))
	}
	if a := c.Author(ctx); a.Name != "Octo Cat" || a.Email != "7+octo@users.noreply.github.com" {
		t.Errorf("author = %+v", a)
	}
	tok, err := c.Token(ctx, "acme/app")
	if err != nil || !strings.HasPrefix(tok, "ghs_") {
		t.Fatalf("token %q, %v", tok, err)
	}
	if again, _ := c.Token(ctx, "acme/app"); again != tok {
		t.Errorf("a valid token was not reused: %q then %q", tok, again)
	}
	if _, err := c.Token(ctx, "acme/lib"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(gh.Minted, " "); got != "1:app 1:lib" {
		t.Errorf("minted %q, want one token per repository", got)
	}

	if _, err := c.Token(ctx, "other/secret"); err == nil {
		t.Error("minted a token through an installation the user can't access")
	}
	if tok, err := c.Token(ctx, "torvalds/linux"); tok != "" || err != nil {
		t.Errorf("public repository without the app: %q, %v; want an anonymous clone", tok, err)
	}
	if _, err := c.Token(ctx, "acme/missing"); err == nil || !strings.Contains(err.Error(), "/apps/beethoven-test/installations/new") {
		t.Errorf("uninstalled private repository: %v; want a pointer to the installation page", err)
	}

	repos, err := c.Repos(ctx)
	if err != nil || len(repos) != 2 {
		t.Errorf("repos = %v, %v; want the user's installation's two", repos, err)
	}
	if _, err := c.OpenPR(ctx, session.PullRequest{Repo: "acme/app", Head: "b", Base: "main", Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if len(gh.PRAuth) != 1 || !strings.HasPrefix(gh.PRAuth[0], "Bearer ghu_") {
		t.Errorf("pull request opened with %v, want the user's token", gh.PRAuth)
	}

	st := c.Status(ctx)
	if st.Account == nil || st.Account.Login != "octo" || len(st.Installations) != 1 || st.App.Source != "settings" {
		t.Errorf("status = %+v", st)
	}
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Mode(ctx) != "token" || len(gh.Revoked) != 1 {
		t.Errorf("after disconnecting: mode %q, revoked %v", c.Mode(ctx), gh.Revoked)
	}
}

func TestExpiredUserTokenIsRefreshedOnce(t *testing.T) {
	gh := fake.NewGitHub(t)
	gh.ExpiresIn = 60 // inside the refresh margin: every token is already expiring
	gh.Install("acme/app", 1, true)
	c := connected(t, gh)

	for range 2 {
		if _, err := c.DefaultBranch(ctx, "acme/app"); err != nil {
			t.Fatal(err)
		}
	}
	if gh.Refreshes != 2 {
		t.Errorf("refreshes = %d, want one per call while tokens keep expiring", gh.Refreshes)
	}

	gh.ExpiresIn = 8 * 3600
	c.DefaultBranch(ctx, "acme/app") // refreshes into a long-lived token
	before := gh.Refreshes
	if _, err := c.DefaultBranch(ctx, "acme/app"); err != nil || gh.Refreshes != before {
		t.Errorf("refreshed a fresh token: %v, %d refreshes", err, gh.Refreshes-before)
	}
}

func TestPreconfiguredAppWins(t *testing.T) {
	gh := fake.NewGitHub(t)
	c := client(t, gh)
	app, err := github.LoadApp(string(gh.AppJSON()))
	if err != nil {
		t.Fatal(err)
	}
	c.EnvApp = app
	if got, source := c.App(ctx); got != app || source != "environment" {
		t.Errorf("app from %q", source)
	}
	if err := c.ForgetApp(ctx); err == nil {
		t.Error("forgot a preconfigured app")
	}
	if _, err := github.ParseApp([]byte(`{"id":1,"slug":"x","client_id":"c","client_secret":"s","pem":"nope"}`)); err == nil {
		t.Error("parsed an app without a private key")
	}
}
