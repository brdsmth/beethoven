package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brdsmth/beethoven/internal/api"
	"github.com/brdsmth/beethoven/internal/automation"
	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/github"
	"github.com/brdsmth/beethoven/internal/slack"
)

// TestConnectSlack creates the app with a configuration token, then
// installs it through the browser round trip.
func TestConnectSlack(t *testing.T) {
	fs := fake.NewSlack(t)
	env := fake.NewEnv(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sl := &slack.Client{API: fs.URL + "/api", Web: fs.URL, DB: env.DB}
	bot := slack.NewBot(sl, env.M, func(context.Context) ([]string, error) { return nil, nil }, log)
	srv := httptest.NewServer((&api.Server{M: env.M, Auto: automation.New(env.M, log), GitHub: github.New("t"), Slack: sl, SlackBot: bot, Log: log}).Handler())
	t.Cleanup(srv.Close)
	const base = "https://beethoven.example.com"

	for _, bad := range []string{"http://beethoven.example.com", "https://localhost:7070", "https://10.0.0.5"} {
		if code, out := do(t, "POST", srv.URL+"/api/slack/app", `{"config_token":"xoxe.xoxp-config","base_url":"`+bad+`"}`); code != 400 {
			t.Errorf("%s: %d %v", bad, code, out)
		}
	}
	code, out := do(t, "POST", srv.URL+"/api/slack/app", `{"config_token":"xoxe.xoxp-config","base_url":"`+base+`"}`)
	if code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	next := out["next"].(string)
	if !strings.HasPrefix(next, fs.URL+"/oauth/v2/authorize?") || query(t, next, "redirect_uri") != base+"/slack/installed" {
		t.Errorf("install at %s", next)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/slack/installed", `{"code":"slack-code","state":"forged"}`); code != 400 {
		t.Errorf("forged state: %d", code)
	}
	code, out = do(t, "POST", srv.URL+"/api/slack/installed", `{"code":"slack-code","state":"`+query(t, next, "state")+`"}`)
	if code != 200 || len(out["installations"].([]any)) != 1 {
		t.Fatalf("installed: %d %v", code, out)
	}

	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).
		Get(srv.URL + "/slack/installed?code=c&state=s")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/?code=c&slack=installed&state=s" {
		t.Errorf("return to %q", loc)
	}
	if code, _ := do(t, "DELETE", srv.URL+"/api/slack/installations/T1", ""); code != http.StatusNoContent {
		t.Errorf("uninstall: %d", code)
	}
}
