package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brdsmth/beethoven/internal/api"
	"github.com/brdsmth/beethoven/internal/automation"
	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/github"
)

// browser is a client with its own cookies.
type browser struct {
	t   *testing.T
	url string
	c   *http.Client
}

func newBrowser(t *testing.T, url string) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t, url, &http.Client{Jar: jar}}
}

func (b *browser) do(method, path, body string) (int, map[string]any, *http.Response) {
	b.t.Helper()
	req, _ := http.NewRequest(method, b.url+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out, resp
}

func TestSignIn(t *testing.T) {
	env := fake.NewEnv(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &api.Server{M: env.M, Auto: automation.New(env.M, log), GitHub: github.New("test-token"), SignIn: true, Log: log}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	me, stranger := newBrowser(t, srv.URL), newBrowser(t, srv.URL)

	if _, st, _ := me.do("GET", "/api/auth", ""); st["required"] != true || st["setup"] != true || st["account"] != nil {
		t.Errorf("new server: %v, want sign-in required and setup offered", st)
	}
	if code, _, _ := me.do("GET", "/api/sessions", ""); code != http.StatusUnauthorized {
		t.Errorf("signed out: %d, want 401", code)
	}
	if code, _, _ := me.do("POST", "/api/auth/login", `{"email":"me@example.com","password":"correct horse"}`); code != http.StatusUnauthorized {
		t.Errorf("sign in before any account: %d, want 401", code)
	}
	if code, out, _ := me.do("POST", "/api/auth/setup", `{"name":"Me","email":"me@example.com","password":"short"}`); code != http.StatusBadRequest {
		t.Errorf("short password: %d %v, want 400", code, out)
	}

	code, out, resp := me.do("POST", "/api/auth/setup", `{"name":"Me","email":" Me@Example.com ","password":"correct horse"}`)
	if code != http.StatusCreated {
		t.Fatalf("setup: %d %v", code, out)
	}
	if a, _ := out["account"].(map[string]any); a["email"] != "me@example.com" || a["owner"] != true {
		t.Errorf("owner account: %v", out)
	}
	cookie := resp.Header.Get("Set-Cookie")
	if !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "SameSite=Lax") {
		t.Errorf("cookie %q, want HttpOnly and SameSite=Lax", cookie)
	}
	if code, _, _ := me.do("GET", "/api/sessions", ""); code != http.StatusOK {
		t.Errorf("signed in: %d, want 200", code)
	}
	if _, st, _ := me.do("GET", "/api/auth", ""); st["setup"] != false || st["account"] == nil {
		t.Errorf("after setup: %v", st)
	}
	if code, _, _ := stranger.do("POST", "/api/auth/setup", `{"email":"x@example.com","password":"another one"}`); code != http.StatusConflict {
		t.Errorf("second setup: %d, want 409", code)
	}
	if code, _, _ := stranger.do("GET", "/api/sessions", ""); code != http.StatusUnauthorized {
		t.Errorf("another browser: %d, want 401", code)
	}

	if code, _, _ := me.do("POST", "/api/auth/logout", ""); code != http.StatusNoContent {
		t.Errorf("sign out: %d", code)
	}
	if code, _, _ := me.do("GET", "/api/sessions", ""); code != http.StatusUnauthorized {
		t.Errorf("after signing out: %d, want 401", code)
	}
	if code, _, _ := me.do("POST", "/api/auth/login", `{"email":"me@example.com","password":"wrong horse"}`); code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d, want 401", code)
	}
	if code, _, _ := me.do("POST", "/api/auth/login", `{"email":"ME@example.com","password":"correct horse"}`); code != http.StatusOK {
		t.Errorf("sign in: %d", code)
	}
	if code, _, _ := me.do("GET", "/api/sessions", ""); code != http.StatusOK {
		t.Errorf("signed in again: %d, want 200", code)
	}

	for range 10 {
		stranger.do("POST", "/api/auth/login", `{"email":"me@example.com","password":"guess"}`)
	}
	if code, _, _ := stranger.do("POST", "/api/auth/login", `{"email":"me@example.com","password":"correct horse"}`); code != http.StatusTooManyRequests {
		t.Errorf("after 10 failures: %d, want 429", code)
	}
}

func TestLocalServerDoesNotAskToSignIn(t *testing.T) {
	srv, _ := server(t, "")
	b := newBrowser(t, srv.URL)
	if _, st, _ := b.do("GET", "/api/auth", ""); st["required"] != false || st["setup"] != false {
		t.Errorf("local server: %v", st)
	}
	if code, _, _ := b.do("POST", "/api/auth/setup", `{"email":"me@example.com","password":"correct horse"}`); code != http.StatusBadRequest {
		t.Errorf("setup on a local server: %d, want 400", code)
	}
}
