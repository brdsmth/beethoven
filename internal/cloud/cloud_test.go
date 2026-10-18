package cloud

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/github"
	"github.com/brdsmth/beethoven/internal/sandbox"
	"github.com/brdsmth/beethoven/internal/slack"
	"github.com/brdsmth/beethoven/internal/store"
	"github.com/brdsmth/beethoven/internal/workspace"
)

var ctx = context.Background()

const adminToken = "operator-token-0123456789"

type harness struct {
	svc   *Service
	slack *fake.Slack
	dir   string
}

func setup(t *testing.T) *harness {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	gh := fake.NewGitHub(t)
	app, err := github.LoadApp(string(gh.AppJSON()))
	if err != nil {
		t.Fatal(err)
	}
	fs := fake.NewSlack(t)
	sapp, err := slack.ParseApp([]byte(`{"app_id":"A1","client_id":"1.2","client_secret":"csecret","signing_secret":"ssecret"}`))
	if err != nil {
		t.Fatal(err)
	}
	box := fake.NewProvider()
	dir := t.TempDir()
	svc, err := New(Config{
		Domain: "example.test", DataDir: dir, MasterKey: bytes.Repeat([]byte("m"), 32), AdminToken: adminToken,
		Workspace: workspace.Config{Image: "test-image", IdleTimeout: time.Hour, TurnTimeout: time.Minute, GitHubApp: app,
			SlackApp: sapp, GitHubAPI: gh.URL, GitHubWeb: gh.URL, SlackAPI: fs.URL + "/api", SlackWeb: fs.URL, Log: log,
			Author: store.Author{Name: "Beethoven", Email: "b@example.test"}},
		Sandboxes: func(string) (sandbox.Provider, error) { return box, nil },
		ToolsBase: "http://tools.test", Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close(5 * time.Second) })
	return &harness{svc: svc, slack: fs, dir: dir}
}

// visitor is a browser on one host, with its own cookies.
type visitor struct {
	t       *testing.T
	svc     *Service
	host    string
	cookies map[string]string
}

func (h *harness) visit(t *testing.T, host string) *visitor {
	return &visitor{t: t, svc: h.svc, host: host, cookies: map[string]string{}}
}

func (v *visitor) do(method, path, body string, header ...string) (int, map[string]any, http.Header) {
	v.t.Helper()
	req := httptest.NewRequest(method, "https://"+v.host+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	for k, val := range v.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: val})
	}
	rec := httptest.NewRecorder()
	v.svc.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		v.cookies[c.Name] = c.Value
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out == nil {
		out = map[string]any{"body": rec.Body.String()}
	}
	return rec.Code, out, rec.Header()
}

func code(link string) string { return link[strings.LastIndex(link, "/")+1:] }

func TestWorkspacesAreSeparate(t *testing.T) {
	h := setup(t)
	acme, acmeLink, err := h.svc.Create(ctx, "acme", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	_, globexLink, _ := h.svc.Create(ctx, "globex", "Globex")
	if !strings.HasPrefix(acmeLink, "https://acme.example.test/#/claim/") {
		t.Errorf("claim link %q", acmeLink)
	}
	for _, bad := range []string{"app", "a", "Acme", "-x-", "acme"} {
		if _, _, err := h.svc.Create(ctx, bad, "x"); err == nil {
			t.Errorf("created %q", bad)
		}
	}

	a := h.visit(t, "acme.example.test")
	if _, st, _ := a.do("GET", "/api/auth", ""); st["setup"] != true || st["claim"] != true {
		t.Errorf("a new workspace: %v", st)
	}
	creds := `{"name":"Ann","email":"ann@acme.test","password":"correct horse"`
	if c, _, _ := a.do("POST", "/api/auth/setup", creds+`}`); c != http.StatusForbidden {
		t.Errorf("claimed without the code: %d", c)
	}
	if c, _, _ := a.do("POST", "/api/auth/setup", creds+`,"code":"`+code(globexLink)+`"}`); c != http.StatusForbidden {
		t.Errorf("claimed with another workspace's code: %d", c)
	}
	if c, out, _ := a.do("POST", "/api/auth/setup", creds+`,"code":"`+code(acmeLink)+`"}`); c != http.StatusCreated {
		t.Fatalf("claim: %d %v", c, out)
	}
	if c, out, _ := a.do("POST", "/api/sessions", `{"prompt":"hello"}`); c != http.StatusCreated {
		t.Fatalf("session: %d %v", c, out)
	}

	g := h.visit(t, "globex.example.test")
	g.do("POST", "/api/auth/setup", `{"email":"gus@globex.test","password":"correct horse","code":"`+code(globexLink)+`"}`)
	if c, out, _ := g.do("GET", "/api/sessions", ""); c != 200 || out["body"] != "[]\n" {
		t.Errorf("globex's sessions: %d %v", c, out)
	}
	// Acme's cookie means nothing at globex.
	g.cookies = a.cookies
	if c, _, _ := g.do("GET", "/api/sessions", ""); c != http.StatusUnauthorized {
		t.Errorf("acme's login at globex: %d", c)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "workspaces", acme.ID, "beethoven.db")); err != nil {
		t.Errorf("acme's database: %v", err)
	}
	if _, info, _ := a.do("GET", "/api/info", ""); info["cloud"] != true {
		t.Errorf("info = %v", info)
	}
	// The cloud runs on API keys, not subscriptions.
	if c, _, _ := a.do("PUT", "/api/credentials", `{"name":"CLAUDE_CODE_OAUTH_TOKEN","value":"x"}`); c != http.StatusBadRequest {
		t.Errorf("a subscription token: %d", c)
	}

	if c, _, _ := h.visit(t, "nobody.example.test").do("GET", "/", ""); c != http.StatusNotFound {
		t.Errorf("an unknown workspace: %d", c)
	}
	if c, _, _ := h.visit(t, "elsewhere.test").do("GET", "/", ""); c != http.StatusNotFound {
		t.Errorf("an unknown host: %d", c)
	}
	if c, _, _ := h.visit(t, "app.example.test").do("GET", "/", ""); c != 200 {
		t.Errorf("the service's page: %d", c)
	}
	if c, _, _ := h.visit(t, "acme.example.test").do("POST", "/ws/acme/mcp", `{}`); c == http.StatusNotFound {
		t.Error("a sandbox can't reach its workspace's tools")
	}
	if c, _, _ := h.visit(t, "host.docker.internal").do("POST", "/ws/acme/other", ``); c != http.StatusNotFound {
		t.Errorf("/ws/ beyond the tools: %d", c)
	}
}

// TestMigratingBacksUpFirst opens a workspace whose database is a
// migration behind: it is copied aside, then migrated.
func TestMigratingBacksUpFirst(t *testing.T) {
	h := setup(t)
	ws, _, _ := h.svc.Create(ctx, "acme", "Acme")
	if err := h.svc.Retry(ctx, ws.ID); err != nil {
		t.Fatal(err)
	}
	h.svc.stop(ws.ID)
	path := filepath.Join(h.dir, "workspaces", ws.ID, "beethoven.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var last string
	db.QueryRow(`SELECT name FROM schema_migrations ORDER BY name DESC LIMIT 1`).Scan(&last)
	if _, err := db.Exec(`DROP TABLE audit_events; DELETE FROM schema_migrations WHERE name='010_audit.sql'`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if err := h.svc.Retry(ctx, ws.ID); err != nil {
		t.Fatal(err)
	}
	backups, _ := os.ReadDir(filepath.Join(h.dir, "workspaces", ws.ID, "backups"))
	if len(backups) != 1 || !strings.Contains(backups[0].Name(), "-before-010_audit") {
		t.Errorf("backups = %v", backups)
	}
	got, _ := h.svc.reg.workspace(ctx, ws.ID)
	if got.MigratedAt == nil || got.LastMigration == "" || got.MigrationError != "" || got.Migrations < 10 {
		t.Errorf("recorded %+v", got)
	}
}

func TestAFailingWorkspaceLeavesTheOthers(t *testing.T) {
	h := setup(t)
	bad, _, _ := h.svc.Create(ctx, "broken", "Broken")
	_, goodLink, _ := h.svc.Create(ctx, "fine", "Fine")
	os.MkdirAll(filepath.Join(h.dir, "workspaces", bad.ID), 0o700)
	path := filepath.Join(h.dir, "workspaces", bad.ID, "beethoven.db")
	db, _ := sql.Open("sqlite", path)
	db.Exec(`CREATE TABLE stranger (x INTEGER)`) // not ours: it won't open
	db.Close()

	if c, _, _ := h.visit(t, "broken.example.test").do("GET", "/api/auth", ""); c != http.StatusServiceUnavailable {
		t.Errorf("the broken workspace: %d", c)
	}
	got, _ := h.svc.reg.workspace(ctx, bad.ID)
	if !strings.Contains(got.MigrationError, "not a beethoven database") {
		t.Errorf("recorded error %q", got.MigrationError)
	}
	f := h.visit(t, "fine.example.test")
	if c, _, _ := f.do("POST", "/api/auth/setup", `{"email":"f@fine.test","password":"correct horse","code":"`+code(goodLink)+`"}`); c != 201 {
		t.Errorf("the other workspace: %d", c)
	}

	os.Remove(path) // the operator moves the stranger aside and retries
	if err := h.svc.Retry(ctx, bad.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.svc.reg.workspace(ctx, bad.ID); got.MigrationError != "" {
		t.Errorf("after the retry: %q", got.MigrationError)
	}
}

func TestSuspendedWorkspacesRefuse(t *testing.T) {
	h := setup(t)
	ws, _, _ := h.svc.Create(ctx, "acme", "Acme")
	if err := h.svc.SetStatus(ctx, ws.ID, StatusSuspended); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := h.visit(t, "acme.example.test").do("GET", "/api/auth", ""); c != http.StatusForbidden {
		t.Errorf("suspended: %d", c)
	}
	h.svc.SetStatus(ctx, ws.ID, StatusActive)
	if c, _, _ := h.visit(t, "acme.example.test").do("GET", "/api/auth", ""); c != 200 {
		t.Errorf("reactivated: %d", c)
	}
}

func TestReturnsFindTheirWorkspace(t *testing.T) {
	h := setup(t)
	h.svc.Create(ctx, "acme", "Acme")
	app := h.visit(t, "app.example.test")
	c, _, hdr := app.do("GET", "/github/authorized?code=x&state=acme~abc", "")
	if c != http.StatusFound || hdr.Get("Location") != "https://acme.example.test/github/authorized?code=x&state=acme~abc" {
		t.Errorf("GitHub's return: %d to %q", c, hdr.Get("Location"))
	}
	c, _, hdr = app.do("GET", "/slack/installed?code=y&state=acme~def", "")
	if c != http.StatusFound || !strings.HasPrefix(hdr.Get("Location"), "https://acme.example.test/slack/installed?") {
		t.Errorf("Slack's return: %d to %q", c, hdr.Get("Location"))
	}
	if c, _, _ := app.do("GET", "/github/installed?installation_id=1", ""); c != http.StatusBadRequest {
		t.Errorf("a return without a state: %d", c)
	}
	if c, _, _ := app.do("GET", "/github/authorized?state=nobody~x", ""); c != http.StatusNotFound {
		t.Errorf("a return to an unknown workspace: %d", c)
	}

	// A workspace's links send GitHub back through the service, with
	// state naming it.
	_, link, _ := h.svc.Create(ctx, "globex", "Globex")
	g := h.visit(t, "globex.example.test")
	g.do("POST", "/api/auth/setup", `{"email":"g@globex.test","password":"correct horse","code":"`+code(link)+`"}`)
	_, out, _ := g.do("POST", "/api/github/authorize", `{"base_url":"https://globex.example.test"}`)
	u, _ := url.Parse(out["url"].(string))
	if u.Query().Get("redirect_uri") != "https://app.example.test/github/authorized" || !strings.HasPrefix(u.Query().Get("state"), "globex~") {
		t.Errorf("authorize at %s", out["url"])
	}
	_, st, _ := g.do("GET", "/api/github", "")
	if inst, _ := st["app"].(map[string]any)["install_url"].(string); !strings.HasSuffix(inst, "?state=globex~install") {
		t.Errorf("install link %q", inst)
	}
}

func sign(body []byte) http.Header {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte("ssecret"))
	mac.Write([]byte("v0:" + ts + ":" + string(body)))
	h := http.Header{}
	h.Set("X-Slack-Request-Timestamp", ts)
	h.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
	return h
}

// claimed creates a workspace and claims it, returning its owner's browser.
func (h *harness) claimed(t *testing.T, slug string) (Workspace, *visitor) {
	t.Helper()
	ws, link, err := h.svc.Create(ctx, slug, slug)
	if err != nil {
		t.Fatal(err)
	}
	v := h.visit(t, slug+".example.test")
	if c, out, _ := v.do("POST", "/api/auth/setup", `{"email":"owner@`+slug+`.test","password":"correct horse","code":"`+code(link)+`"}`); c != http.StatusCreated {
		t.Fatalf("claiming %s: %d %v", slug, c, out)
	}
	return ws, v
}

// addSlack adds the fake Slack workspace (T1) from a workspace's settings.
func addSlack(v *visitor) (int, map[string]any) {
	_, out, _ := v.do("POST", "/api/slack/install", `{"base_url":"https://`+v.host+`"}`)
	u, err := url.Parse(fmt.Sprint(out["url"]))
	if err != nil || u.Query().Get("redirect_uri") != "https://app.example.test/slack/installed" {
		v.t.Fatalf("install link %v", out["url"])
	}
	c, out, _ := v.do("POST", "/api/slack/installed", `{"code":"slack-code","state":"`+u.Query().Get("state")+`"}`)
	return c, out
}

func TestSlackEventsGoToTheirWorkspace(t *testing.T) {
	h := setup(t)
	acme, a := h.claimed(t, "acme")
	_, g := h.claimed(t, "globex")
	if c, out := addSlack(a); c != 200 {
		t.Fatalf("adding Slack: %d %v", c, out)
	}
	if c, _ := addSlack(g); c != http.StatusConflict {
		t.Errorf("one Slack workspace added to two: %d", c)
	}
	rt, err := h.svc.runtime(ctx, acme)
	if err != nil {
		t.Fatal(err)
	}

	post := func(path string, body []byte, signed bool) (int, string) {
		req := httptest.NewRequest("POST", "https://app.example.test"+path, bytes.NewReader(body))
		if signed {
			for k, v := range sign(body) {
				req.Header[k] = v
			}
		}
		rec := httptest.NewRecorder()
		h.svc.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	if c, body := post("/slack/events", []byte(`{"type":"url_verification","challenge":"abc"}`), true); c != 200 || body != "abc" {
		t.Errorf("verification: %d %q", c, body)
	}
	ev := []byte(`{"type":"event_callback","team_id":"T1","api_app_id":"A1","event_id":"Ev1","event":{"type":"app_mention",` +
		`"user":"U1","text":"<@UBOT> say hi","ts":"100.000001","channel":"C1"}}`)
	if c, _ := post("/slack/events", ev, false); c != http.StatusUnauthorized {
		t.Errorf("unsigned: %d", c)
	}
	if c, _ := post("/slack/events", ev, true); c != 200 {
		t.Errorf("event: %d", c)
	}
	fake.WaitFor(t, "acme's session", func() bool {
		list, _ := rt.DB.Sessions(ctx, store.SessionFilter{})
		return len(list) == 1
	})
	other := []byte(strings.Replace(string(ev), "T1", "T9", 1))
	if c, _ := post("/slack/events", other, true); c != 200 {
		t.Errorf("an unknown Slack workspace: %d; acknowledge it", c)
	}

	form := url.Values{"payload": {`{"type":"block_actions","team":{"id":"T1"},"user":{"id":"U1"},"actions":[]}`}}.Encode()
	if c, _ := post("/slack/interactions", []byte(form), true); c != 200 {
		t.Errorf("a click: %d", c)
	}

	// Removed from acme, it can go to globex.
	if c, _, _ := a.do("DELETE", "/api/slack/installations/T1", ""); c != http.StatusNoContent {
		t.Fatalf("removing Slack: %d", c)
	}
	if c, out := addSlack(g); c != 200 {
		t.Errorf("adding Slack after acme let go: %d %v", c, out)
	}
}

func TestAdmin(t *testing.T) {
	h := setup(t)
	app := h.visit(t, "app.example.test")
	if c, _, hdr := app.do("GET", "/admin", ""); c != http.StatusUnauthorized || hdr.Get("WWW-Authenticate") == "" {
		t.Errorf("without the token: %d", c)
	}
	basic := func(pass string) string {
		req := httptest.NewRequest("GET", "/", nil)
		req.SetBasicAuth("admin", pass)
		return req.Header.Get("Authorization")
	}
	if c, _, _ := app.do("GET", "/admin", "", "Authorization", basic("wrong")); c != http.StatusUnauthorized {
		t.Errorf("a wrong token: %d", c)
	}
	c, out, _ := app.do("POST", "/admin/workspaces", "slug=acme&name=Acme", "Authorization", basic(adminToken),
		"Content-Type", "application/x-www-form-urlencoded")
	if c != 200 || !strings.Contains(out["body"].(string), "https://acme.example.test/#/claim/") {
		t.Fatalf("create: %d %v", c, out)
	}
	c, out, _ = app.do("GET", "/admin/workspaces.json", "", "Authorization", basic(adminToken))
	if c != 200 || !strings.Contains(out["body"].(string), `"slug":"acme"`) {
		t.Errorf("list: %d %v", c, out)
	}
	if c, _, _ := app.do("POST", "/admin/workspaces", "slug=x&name=y", "Authorization", basic(adminToken),
		"Content-Type", "application/x-www-form-urlencoded", "Sec-Fetch-Site", "cross-site"); c != http.StatusForbidden {
		t.Errorf("a form from another site: %d", c)
	}
}
