package github_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brdsmth/beethoven/internal/access"
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

func TestPullRequestsAreTrackedUntilMerged(t *testing.T) {
	gh := fake.NewGitHub(t)
	gh.Install("acme/app", 1, true)
	c := connected(t, gh)
	s := store.Session{ID: store.NewID(), Title: "t", Branch: "b", Source: "web", Status: "idle", Sandbox: "none",
		Repos: []store.RepoRef{{Repo: "acme/app", PRURL: "https://github.com/acme/app/pull/5"}}}
	if err := c.DB.CreateSession(ctx, &s); err != nil {
		t.Fatal(err)
	}
	gh.SetPull("acme/app", 5, map[string]any{"state": "open", "draft": true, "created_at": "2026-10-01T10:00:00Z"})
	sync := func() store.PullRequest {
		t.Helper()
		if err := c.SyncPullRequests(ctx); err != nil {
			t.Fatal(err)
		}
		prs, _ := c.DB.SessionPullRequests(ctx, s.ID)
		if len(prs) != 1 {
			t.Fatalf("tracking %d pull requests", len(prs))
		}
		return prs[0]
	}

	if p := sync(); p.State != "open" || !p.Draft || p.OpenedAt.UTC().Hour() != 10 {
		t.Errorf("first sync: %+v", p)
	}
	sync()
	if gh.PullReads["304"] != 1 {
		t.Errorf("reads = %v; an unchanged pull request should come back 304", gh.PullReads)
	}
	gh.SetPull("acme/app", 5, map[string]any{"state": "closed", "created_at": "2026-10-01T10:00:00Z",
		"merged_at": "2026-10-01T13:00:00Z", "closed_at": "2026-10-01T13:00:00Z"})
	if p := sync(); p.State != "merged" || p.MergedAt == nil || p.MergedAt.Sub(p.OpenedAt) != 3*time.Hour {
		t.Errorf("after merging: %+v", p)
	}
	reads := gh.PullReads["200"] + gh.PullReads["304"]
	sync()
	if gh.PullReads["200"]+gh.PullReads["304"] != reads {
		t.Error("polled a merged pull request")
	}
}

func TestEachPersonUsesTheirOwnGitHub(t *testing.T) {
	gh := fake.NewGitHub(t)
	gh.Install("acme/app", 1, true)
	c := client(t, gh)
	owner, _ := c.DB.CreateOwner(ctx, store.Account{Email: "o@x.dev"}, "h")
	in := store.Invite{Role: store.RoleMember, CreatedBy: owner.ID}
	tok, _ := c.DB.CreateInvite(ctx, &in)
	ann, _ := c.DB.AcceptInvite(ctx, tok, store.Account{Email: "ann@x.dev"}, "h")
	cy, _ := c.DB.AcceptInvite(ctx, mustInvite(t, c.DB, owner.ID), store.Account{Email: "cy@x.dev"}, "h")
	asOwner, asAnn, asCy := access.For(ctx, owner.ID), access.For(ctx, ann.ID), access.For(ctx, cy.ID)

	// Until anyone connects, the personal access token serves everyone.
	if tok, _ := c.Token(asCy, "acme/app"); tok != "pat" {
		t.Errorf("before anyone connects: %q", tok)
	}
	if _, err := c.RegisterApp(ctx, "manifest-code"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Connect(asOwner, "oauth-code", "http://x/github/authorized"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Connect(asAnn, "oauth-code:annie", "http://x/github/authorized"); err != nil {
		t.Fatal(err)
	}

	if a := c.Author(asAnn); a.Name != "annie" || !strings.Contains(a.Email, "+annie@") {
		t.Errorf("ann commits as %+v", a)
	}
	if a := c.Author(asOwner); a.Name != "Octo Cat" {
		t.Errorf("the owner commits as %+v", a)
	}
	if a := c.Author(ctx); a.Name != "Octo Cat" {
		t.Errorf("work for nobody in particular commits as %+v, want the owner", a)
	}
	// Cy hasn't connected: no stand-in, not even the token.
	if _, err := c.Token(asCy, "acme/app"); !errors.Is(err, github.ErrNotConnected) {
		t.Errorf("cy's token: %v", err)
	}
	if c.Mode(asCy) != "" || c.Mode(asAnn) != "app" {
		t.Errorf("modes: cy %q, ann %q", c.Mode(asCy), c.Mode(asAnn))
	}
	if st := c.Status(asCy); st.Account != nil || st.Token != "" {
		t.Errorf("cy's status = %+v", st)
	}

	if _, err := c.OpenPR(asAnn, session.PullRequest{Repo: "acme/app", Head: "b", Base: "main", Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenPR(asOwner, session.PullRequest{Repo: "acme/app", Head: "c", Base: "main", Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if len(gh.PRAuth) != 2 || gh.PRAuth[0] == gh.PRAuth[1] {
		t.Errorf("pull requests opened with %v; want each person's own token", gh.PRAuth)
	}

	// Disconnecting is per person.
	if err := c.Disconnect(asAnn); err != nil {
		t.Fatal(err)
	}
	if c.Mode(asAnn) != "" || c.Mode(asOwner) != "app" {
		t.Errorf("after ann disconnects: ann %q, owner %q", c.Mode(asAnn), c.Mode(asOwner))
	}
}

func mustInvite(t *testing.T, db *store.Store, by string) string {
	t.Helper()
	in := store.Invite{Role: store.RoleMember, CreatedBy: by}
	tok, err := db.CreateInvite(ctx, &in)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestCreatingRepositories(t *testing.T) {
	gh := fake.NewGitHub(t)
	gh.Install("octo/old", 1, true) // on the user's account, without the permission
	gh.SetAccount(2, "acme", "Organization", map[string]string{"contents": "write", "repository_creation": "write"})
	c := connected(t, gh)

	owners, err := c.Owners(ctx)
	if err != nil || len(owners) != 2 {
		t.Fatalf("owners = %+v, %v", owners, err)
	}
	if o := owners[0]; o.Login != "octo" || o.FixURL != gh.URL+"/settings/apps/beethoven-test/permissions" ||
		!strings.Contains(o.Problem, "Repository creation to Read and write") {
		t.Errorf("the user's account, where the app can't create repositories: %+v", o)
	}
	if o := owners[1]; o.Login != "acme" || o.Type != "Organization" || o.Problem != "" {
		t.Errorf("the organization, where it can: %+v", o)
	}
	gh.AskFor(map[string]string{"repository_creation": "write"}) // the app asks; the user's account hasn't accepted
	if owners, _ := c.Owners(ctx); !strings.Contains(owners[0].Problem, "hasn't accepted") ||
		owners[0].FixURL != gh.URL+"/settings/installations/1" {
		t.Errorf("after the app asks: %+v", owners[0])
	}

	if _, err := c.CreateRepo(ctx, github.NewRepo{Owner: "octo", Name: "nope"}); err == nil ||
		!strings.Contains(err.Error(), "Resource not accessible") {
		t.Errorf("created a repository where the app may not: %v", err)
	}
	c.Repos(ctx) // cached until a repository is created
	repo, err := c.CreateRepo(ctx, github.NewRepo{Owner: "acme", Name: "new-app", Description: "d", Private: true, Gitignore: "Go"})
	if err != nil || repo.Repo != "acme/new-app" || !repo.Private || repo.DefaultBranch != "main" {
		t.Fatalf("created %+v, %v", repo, err)
	}
	if got := gh.Created[0]; got["auto_init"] != true || got["gitignore_template"] != "Go" || got["description"] != "d" {
		t.Errorf("asked GitHub for %v", got)
	}
	if tok, err := c.Token(ctx, "acme/new-app"); err != nil || tok == "" {
		t.Errorf("token for the new repository: %q, %v", tok, err)
	}
	if repos, _ := c.Repos(ctx); !slices.ContainsFunc(repos, func(r github.Repo) bool { return r.Repo == "acme/new-app" }) {
		t.Errorf("repos = %v; the new one is missing", repos)
	}
	if _, err := c.CreateRepo(ctx, github.NewRepo{Owner: "acme", Name: "new-app"}); !errors.Is(err, store.ErrInvalid) ||
		!strings.Contains(err.Error(), "already exists") {
		t.Errorf("created a repository twice: %v", err)
	}
}
