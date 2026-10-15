package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var ctx = context.Background()

func open(t *testing.T) *Store {
	t.Helper()
	key := []byte(strings.Repeat("k", 32))
	s, err := Open(filepath.Join(t.TempDir(), "t.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenRefusesForeignDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE events (seq INTEGER PRIMARY KEY, payload TEXT)`); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path, []byte(strings.Repeat("k", 32)))
	if err == nil {
		s.Close()
		t.Fatal("opened a database beethoven did not create")
	}
	if !strings.Contains(err.Error(), "not a beethoven database") {
		t.Fatalf("err = %v", err)
	}
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "delete" {
		t.Errorf("journal mode = %q: the refused database was changed", mode)
	}
}

func TestReopenKeepsWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	for range 2 { // create, then reopen
		s, err := Open(path, []byte(strings.Repeat("k", 32)))
		if err != nil {
			t.Fatal(err)
		}
		var mode string
		err = s.r.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
		s.Close()
		if err != nil || mode != "wal" {
			t.Fatalf("journal mode = %q, %v; want wal", mode, err)
		}
	}
}

func session(t *testing.T, s *Store) Session {
	t.Helper()
	x := Session{ID: NewID(), Title: "t", Branch: "b", Source: "test", Status: "idle", Sandbox: "none"}
	if err := s.CreateSession(ctx, &x); err != nil {
		t.Fatal(err)
	}
	return x
}

func TestQueueOrderAndClaim(t *testing.T) {
	s := open(t)
	x := session(t, s)
	var ids []string
	for range 3 {
		p := Prompt{ID: NewID(), SessionID: x.ID, Content: "c", Source: "test"}
		if _, err := s.CreatePrompt(ctx, &p); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	for _, want := range ids {
		p, err := s.ClaimNext(ctx, x.ID)
		if err != nil || p.ID != want || p.Status != Running || p.StartedAt == nil {
			t.Fatalf("ClaimNext = %+v, %v; want %s", p, err, want)
		}
	}
	if _, err := s.ClaimNext(ctx, x.ID); err != ErrNotFound {
		t.Errorf("empty queue: err = %v", err)
	}
}

func TestDedupAndCancel(t *testing.T) {
	s := open(t)
	x := session(t, s)
	a := Prompt{ID: NewID(), SessionID: x.ID, Content: "a", Source: "test", DedupKey: "k1"}
	b := Prompt{ID: NewID(), SessionID: x.ID, Content: "b", Source: "test", DedupKey: "k1"}
	if created, _ := s.CreatePrompt(ctx, &a); !created {
		t.Fatal("first prompt not created")
	}
	if created, _ := s.CreatePrompt(ctx, &b); created || b.ID != a.ID || b.Content != "a" {
		t.Fatalf("duplicate: created=%v prompt=%+v", created, b)
	}
	c := Prompt{ID: NewID(), SessionID: x.ID, Content: "c", Source: "test"}
	s.CreatePrompt(ctx, &c)
	got, err := s.CancelQueued(ctx, x.ID, c.ID)
	if err != nil || len(got) != 1 || got[0].ID != c.ID || got[0].Status != Cancelled {
		t.Fatalf("cancel one = %+v, %v", got, err)
	}
	got, _ = s.CancelQueued(ctx, x.ID, "")
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("cancel all = %+v", got)
	}
}

func TestPromptAttachments(t *testing.T) {
	s := open(t)
	x, other := session(t, s), session(t, s)
	png := []byte("\x89PNG\r\n\x1a\n")
	p := Prompt{ID: NewID(), SessionID: x.ID, Content: "look", Source: "test", DedupKey: "k1", Attachments: []Attachment{
		{Name: "shot.png", ContentType: "image/png", Data: png},
		{Name: "empty.txt", ContentType: "text/plain"},
	}}
	if created, err := s.CreatePrompt(ctx, &p); !created || err != nil {
		t.Fatalf("create: %v, %v", created, err)
	}
	if a := p.Attachments[0]; a.ID == "" || a.Size != int64(len(png)) || a.Data != nil {
		t.Errorf("after creating: %+v, want an id, the size and no data", a)
	}
	plain := Prompt{ID: NewID(), SessionID: x.ID, Content: "no files", Source: "test"}
	s.CreatePrompt(ctx, &plain)

	ps, err := s.Prompts(ctx, x.ID)
	if err != nil || len(ps) != 2 {
		t.Fatalf("Prompts = %+v, %v", ps, err)
	}
	if got := ps[0].Attachments; len(got) != 2 || got[0].Name != "shot.png" || got[1].Name != "empty.txt" || got[0].Data != nil {
		t.Errorf("listed attachments = %+v, want both, in order, without data", got)
	}
	if ps[1].Attachments != nil {
		t.Errorf("a prompt without files lists %+v", ps[1].Attachments)
	}
	if one, _ := s.Prompt(ctx, p.ID); len(one.Attachments) != 2 {
		t.Errorf("Prompt lists %+v", one.Attachments)
	}

	files, err := s.PromptFiles(ctx, p.ID)
	if err != nil || len(files) != 2 || string(files[0].Data) != string(png) || len(files[1].Data) != 0 {
		t.Fatalf("PromptFiles = %+v, %v", files, err)
	}
	a, err := s.Attachment(ctx, x.ID, p.Attachments[0].ID)
	if err != nil || a.Name != "shot.png" || a.ContentType != "image/png" || string(a.Data) != string(png) {
		t.Errorf("Attachment = %+v, %v", a, err)
	}
	if _, err := s.Attachment(ctx, other.ID, p.Attachments[0].ID); err != ErrNotFound {
		t.Errorf("through another session: err = %v, want not found", err)
	}

	// A redelivered prompt stores nothing more and answers with the first.
	dup := Prompt{ID: NewID(), SessionID: x.ID, Content: "again", Source: "test", DedupKey: "k1",
		Attachments: []Attachment{{Name: "other.png", Data: png}}}
	if created, err := s.CreatePrompt(ctx, &dup); created || err != nil || dup.ID != p.ID || len(dup.Attachments) != 2 {
		t.Fatalf("duplicate: created=%v err=%v prompt=%+v", created, err, dup)
	}
	var n int
	s.r.QueryRow(`SELECT count(*) FROM attachments`).Scan(&n)
	if n != 2 {
		t.Errorf("%d attachments stored, want 2", n)
	}
}

func TestEventSequenceIsDensePerSession(t *testing.T) {
	s := open(t)
	x, y := session(t, s), session(t, s)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := x.ID
			if i%2 == 1 {
				id = y.ID
			}
			e := Event{Session: id, Type: "t", Data: []byte(`{}`), At: time.Now()}
			if err := s.AppendEvent(ctx, &e); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for _, id := range []string{x.ID, y.ID} {
		evs, _ := s.Events(ctx, id, 0, 100)
		for i, e := range evs {
			if e.Seq != int64(i+1) {
				t.Fatalf("session %s event %d has seq %d", id, i, e.Seq)
			}
		}
	}
	if evs, _ := s.Events(ctx, x.ID, 20, 100); len(evs) != 5 || evs[0].Seq != 21 {
		t.Errorf("Events(after=20) = %d events starting at %v", len(evs), evs)
	}
}

func TestSecretsAreEncryptedAndLayered(t *testing.T) {
	s := open(t)
	s.SetSecret(ctx, "", "TOKEN", "global-value")
	s.SetSecret(ctx, "", "ONLY_GLOBAL", "g")
	s.SetSecret(ctx, "acme/app", "TOKEN", "repo-value")
	s.SetSecret(ctx, "acme/other", "TOKEN", "other-value")

	env, err := s.SecretEnv(ctx, SecretTarget{Repos: []string{"acme/app"}})
	if err != nil {
		t.Fatal(err)
	}
	if env["TOKEN"] != "repo-value" || env["ONLY_GLOBAL"] != "g" || len(env) != 2 {
		t.Errorf("env = %v", env)
	}
	var raw []byte
	s.r.QueryRow(`SELECT value FROM secrets WHERE scope='' AND name='TOKEN'`).Scan(&raw)
	if strings.Contains(string(raw), "global-value") {
		t.Error("secret stored in plaintext")
	}
	wrongKey := &Store{r: s.r, w: s.w, key: []byte(strings.Repeat("x", 32))}
	if _, err := wrongKey.SecretEnv(ctx, SecretTarget{}); err == nil {
		t.Error("decrypted with the wrong key")
	}
}

func TestAdvanceScheduleFiresOnce(t *testing.T) {
	s := open(t)
	due := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	a := Automation{ID: NewID(), Name: "n", Prompt: "p", Schedule: "* * * * *", Enabled: true, NextRunAt: &due, HookHash: "h"}
	if err := s.CreateAutomation(ctx, &a); err != nil {
		t.Fatal(err)
	}
	list, _ := s.DueAutomations(ctx, time.Now())
	if len(list) != 1 {
		t.Fatalf("due = %d", len(list))
	}
	next := time.Now().Add(time.Minute)
	if ok, _ := s.AdvanceSchedule(ctx, a.ID, *list[0].NextRunAt, next); !ok {
		t.Fatal("first advance failed")
	}
	if ok, _ := s.AdvanceSchedule(ctx, a.ID, *list[0].NextRunAt, next); ok {
		t.Fatal("second advance from the same due time succeeded")
	}
}

func TestGitHubAccounts(t *testing.T) {
	s := open(t)
	if _, err := s.GitHubAccount(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	expires := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	first := GitHubAccount{AppID: 1, ID: 7, Login: "octo", Tokens: GitHubTokens{Access: "ghu_a", AccessExpires: expires, Refresh: "ghr_a"}}
	if err := s.SaveGitHubAccount(ctx, &first); err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	s.r.QueryRow(`SELECT tokens FROM github_accounts`).Scan(&sealed)
	if strings.Contains(string(sealed), "ghu_a") {
		t.Error("tokens are stored in the clear")
	}
	got, err := s.GitHubAccount(ctx, 1)
	if err != nil || got.Login != "octo" || got.Tokens.Access != "ghu_a" || !got.Tokens.AccessExpires.Equal(expires) {
		t.Fatalf("got %+v, %v", got, err)
	}

	time.Sleep(2 * time.Millisecond)
	second := GitHubAccount{AppID: 1, ID: 8, Login: "hubot", Tokens: GitHubTokens{Access: "ghu_b"}}
	s.SaveGitHubAccount(ctx, &second)
	other := GitHubAccount{AppID: 2, ID: 9, Login: "elsewhere", Tokens: GitHubTokens{Access: "ghu_c"}}
	s.SaveGitHubAccount(ctx, &other)
	if got, _ := s.GitHubAccount(ctx, 1); got.Login != "hubot" {
		t.Errorf("acting for %q, want the most recently connected", got.Login)
	}
	first.Tokens.Access = "ghu_refreshed" // a refresh keeps the connection time
	s.SaveGitHubAccount(ctx, &first)
	if got, _ := s.GitHubAccount(ctx, 1); got.Login != "hubot" {
		t.Errorf("a refresh made @%s the current account", got.Login)
	}

	if err := s.DeleteGitHubAccounts(ctx, 1, 8); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GitHubAccount(ctx, 1); got.Tokens.Access != "ghu_refreshed" {
		t.Errorf("after deleting one: %+v", got)
	}
	s.DeleteGitHubAccounts(ctx, 1, 0)
	if _, err := s.GitHubAccount(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("app 1 still has an account: %v", err)
	}
	if _, err := s.GitHubAccount(ctx, 2); err != nil {
		t.Errorf("another app's account went too: %v", err)
	}
}

func TestTrackNewPullRequests(t *testing.T) {
	s := open(t)
	x := Session{ID: NewID(), Title: "t", Branch: "b", Source: "web", Status: "idle", Sandbox: "none", Repos: []RepoRef{
		{Repo: "acme/app", PRURL: "https://github.com/acme/app/pull/7"},
		{Repo: "acme/web"}, // no pull request yet
		{Repo: "acme/odd", PRURL: "https://example.com/not-github"},
	}}
	if err := s.CreateSession(ctx, &x); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		n, err := s.TrackNewPullRequests(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if prs, _ := s.SessionPullRequests(ctx, x.ID); len(prs) != 1 || prs[0].Number != 7 || prs[0].Repo != "acme/app" || prs[0].State != "open" {
			t.Fatalf("tracked %+v (added %d)", prs, n)
		}
	}
	now := time.Now()
	if prs, _ := s.PullRequestsOf(ctx, now.Add(-time.Hour), now.Add(time.Hour)); len(prs) != 1 {
		t.Errorf("in the window: %d", len(prs))
	}
	if prs, _ := s.PullRequestsOf(ctx, now.Add(-48*time.Hour), now.Add(-24*time.Hour)); len(prs) != 0 {
		t.Errorf("outside the window: %d", len(prs))
	}
}

func TestAccountsAndLogins(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if has, err := s.HasAccounts(ctx); err != nil || has {
		t.Fatalf("new store: %v %v", has, err)
	}
	a, err := s.CreateOwner(ctx, Account{Email: "me@example.com", Name: "Me"}, "hash")
	if err != nil || !a.Owner {
		t.Fatalf("owner: %+v %v", a, err)
	}
	if _, err := s.CreateOwner(ctx, Account{Email: "you@example.com"}, "hash"); !errors.Is(err, ErrConflict) {
		t.Errorf("second owner: %v, want ErrConflict", err)
	}
	if got, hash, err := s.AccountByEmail(ctx, "ME@example.com"); err != nil || got.ID != a.ID || hash != "hash" {
		t.Errorf("by email: %+v %q %v", got, hash, err)
	}
	if _, _, err := s.AccountByEmail(ctx, "nobody@example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown email: %v", err)
	}

	if err := s.CreateLogin(ctx, "l1", a.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.LoginAccount(ctx, "l1"); err != nil || got.Email != "me@example.com" {
		t.Errorf("login: %+v %v", got, err)
	}
	s.CreateLogin(ctx, "old", a.ID, time.Now().Add(-time.Minute))
	if _, err := s.LoginAccount(ctx, "old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired login: %v", err)
	}
	s.DeleteLogin(ctx, "l1")
	if _, err := s.LoginAccount(ctx, "l1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after signing out: %v", err)
	}
}
