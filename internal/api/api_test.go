package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brdsmth/beethoven/internal/api"
	"github.com/brdsmth/beethoven/internal/automation"
	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/github"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

func server(t *testing.T, token string) (*httptest.Server, *fake.Env) {
	env := fake.NewEnv(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &api.Server{M: env.M, Auto: automation.New(env.M, log), GitHub: github.New("test-token"), Token: token, Log: log}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, env
}

func do(t *testing.T, method, url, body string, header ...string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(header); i += 2 {
		if header[i] == "Host" {
			req.Host = header[i+1]
		} else {
			req.Header.Set(header[i], header[i+1])
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestGuards(t *testing.T) {
	srv, _ := server(t, "")
	if code, _ := do(t, "GET", srv.URL+"/api/sessions", "", "Host", "evil.example:7070"); code != http.StatusForbidden {
		t.Errorf("foreign Host: %d, want 403", code)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/sessions", `{"prompt":"x"}`, "Sec-Fetch-Site", "cross-site"); code != http.StatusForbidden {
		t.Errorf("cross-site POST: %d, want 403", code)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/sessions", `{"prompt":"x","bogus":1}`); code != http.StatusBadRequest {
		t.Errorf("unknown field: %d, want 400", code)
	}

	srv, _ = server(t, "s3cret")
	if code, _ := do(t, "GET", srv.URL+"/api/sessions", ""); code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", code)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/sessions", "", "Authorization", "Bearer s3cret"); code != http.StatusOK {
		t.Errorf("bearer token: %d, want 200", code)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/sessions?token=s3cret", ""); code != http.StatusOK {
		t.Errorf("query token: %d, want 200", code)
	}
}

func TestSessionLifecycleAndStream(t *testing.T) {
	srv, env := server(t, "")
	code, out := do(t, "POST", srv.URL+"/api/sessions", `{"prompt":"hello","repo":"acme/app"}`)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	id := out["session"].(map[string]any)["id"].(string)
	pid := out["prompt"].(map[string]any)["id"].(string)
	fake.WaitFor(t, "prompt done", func() bool { p, _ := env.DB.Prompt(context.Background(), pid); return p.Status == store.Done })

	// Resume from seq 3: the stream starts with the session state, then seq 4 onward.
	req, _ := http.NewRequest("GET", srv.URL+"/api/sessions/"+id+"/events", nil)
	req.Header.Set("Last-Event-ID", "3")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	var events []store.Event
	for sc.Scan() && len(events) < 3 {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var e store.Event
			json.Unmarshal([]byte(data), &e)
			events = append(events, e)
		}
	}
	if events[0].Type != "session" || events[0].Seq != 0 || events[1].Seq != 4 || events[2].Seq != 5 {
		t.Errorf("stream = %+v", events)
	}

	code, out = do(t, "GET", srv.URL+"/api/sessions/"+id, "")
	if code != 200 || len(out["prompts"].([]any)) != 1 {
		t.Errorf("get: %d %v", code, out)
	}
	if code, out = do(t, "POST", srv.URL+"/api/sessions/"+id+"/pr", `{"title":"","body":"","draft":true}`); code != 200 || out["url"] != "https://github.com/acme/app/pull/1" {
		t.Errorf("pr: %d %v", code, out)
	}
	if code, _ = do(t, "DELETE", srv.URL+"/api/sessions/"+id, ""); code != http.StatusNotFound {
		t.Errorf("delete: %d, want sessions to be archived instead", code)
	}
}

func TestWebhook(t *testing.T) {
	srv, _ := server(t, "api-token") // hooks use their own token, not the API's
	code, out := do(t, "POST", srv.URL+"/api/automations", `{"name":"deploy check","prompt":"look","enabled":true}`,
		"Authorization", "Bearer api-token")
	if code != http.StatusCreated {
		t.Fatalf("create automation: %d %v", code, out)
	}
	hook := srv.URL + out["hook_path"].(string)
	token := out["hook_token"].(string)

	if code, _ := do(t, "POST", hook, `{}`, "Authorization", "Bearer wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", code)
	}
	if code, _ := do(t, "POST", srv.URL+"/hooks/nope", `{}`, "Authorization", "Bearer "+token); code != http.StatusUnauthorized {
		t.Errorf("unknown hook: %d", code)
	}
	code, first := do(t, "POST", hook, `{"ref":"main"}`, "Authorization", "Bearer "+token, "Idempotency-Key", "d1")
	if code != http.StatusAccepted {
		t.Fatalf("hook: %d %v", code, first)
	}
	_, again := do(t, "POST", hook, `{"ref":"main"}`, "Authorization", "Bearer "+token, "Idempotency-Key", "d1")
	if again["prompt_id"] != first["prompt_id"] {
		t.Errorf("redelivery fired again: %v vs %v", again, first)
	}
}

func TestSecretsAPI(t *testing.T) {
	srv, _ := server(t, "")
	if code, _ := do(t, "PUT", srv.URL+"/api/secrets", `{"scope":"","name":"BAD NAME","value":"v"}`); code != http.StatusBadRequest {
		t.Errorf("invalid name: %d", code)
	}
	if code, _ := do(t, "PUT", srv.URL+"/api/secrets", `{"scope":"acme/app","name":"API_KEY","value":"super-secret"}`); code != http.StatusNoContent {
		t.Errorf("put: %d", code)
	}
	resp, _ := http.Get(srv.URL + "/api/secrets")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "API_KEY") || strings.Contains(string(body), "super-secret") {
		t.Errorf("list = %s", body)
	}
}

func TestInsights(t *testing.T) {
	srv, env := server(t, "")
	s, p, err := env.M.Submit(context.Background(), session.Request{Prompt: "hello", Source: "web"})
	if err != nil {
		t.Fatal(err)
	}
	fake.WaitFor(t, "prompt done", func() bool { got, _ := env.DB.Prompt(context.Background(), p.ID); return got.Status == store.Done })
	code, out := do(t, "GET", srv.URL+"/api/insights?days=7&tz=America/New_York", "")
	if code != 200 || out["sessions"] != 1.0 || out["first_try_rate"] != 1.0 || len(out["days"].([]any)) != 7 {
		t.Errorf("insights: %d %v", code, out)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/insights?days=1000", ""); code != http.StatusBadRequest {
		t.Errorf("1000 days: %d", code)
	}
	if code, out = do(t, "GET", srv.URL+"/api/sessions/"+s.ID, ""); code != 200 || out["pull_requests"] == nil {
		t.Errorf("session: %d %v", code, out)
	}
}

func TestHealthz(t *testing.T) {
	srv, _ := server(t, "s3cret")
	resp, err := http.Get(srv.URL + "/healthz") // no token, any Host
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("healthz: %d", resp.StatusCode)
	}
}

func TestSessionSearch(t *testing.T) {
	srv, env := server(t, "")
	for _, x := range []struct {
		title    string
		archived bool
	}{{"Redesign the sidebar", false}, {"Collapse the sidebar", true}, {"Unrelated", false}} {
		s := store.Session{ID: store.NewID(), Title: x.title, Branch: "b", Source: "test", Status: "idle", Sandbox: "none", Archived: x.archived}
		if err := env.DB.CreateSession(context.Background(), &s); err != nil {
			t.Fatal(err)
		}
	}
	titles := func(query string) []string {
		resp, err := http.Get(srv.URL + "/api/sessions?" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var list []store.Session
		json.NewDecoder(resp.Body).Decode(&list)
		var out []string
		for _, s := range list {
			out = append(out, s.Title)
		}
		return out
	}
	if got := titles("q=sidebar"); len(got) != 1 || got[0] != "Redesign the sidebar" {
		t.Errorf("active sessions matching: %v", got)
	}
	if got := titles("q=SIDEBAR&archived=all"); len(got) != 2 {
		t.Errorf("all sessions matching: %v", got)
	}
	if got := titles("archived=1"); len(got) != 1 || got[0] != "Collapse the sidebar" {
		t.Errorf("archived sessions: %v", got)
	}
}
