package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/brdsmth/beethoven/internal/api"
	"github.com/brdsmth/beethoven/internal/automation"
	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/github"
)

// TestConnectGitHub walks the browser's round trips: register the app,
// authorize it, then install it.
func TestConnectGitHub(t *testing.T) {
	gh := fake.NewGitHub(t)
	env := fake.NewEnv(t)
	client := github.New("")
	client.API, client.Web, client.DB = gh.URL, gh.URL, env.DB
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer((&api.Server{M: env.M, Auto: automation.New(env.M, log), GitHub: client, Log: log}).Handler())
	t.Cleanup(srv.Close)
	const base = "http://beethoven.test"

	code, out := do(t, "POST", srv.URL+"/api/github/app", `{"base_url":"`+base+`/"}`)
	if code != 200 {
		t.Fatalf("register: %d %v", code, out)
	}
	var manifest struct {
		RedirectURL  string   `json:"redirect_url"`
		CallbackURLs []string `json:"callback_urls"`
		SetupURL     string   `json:"setup_url"`
		Public       bool     `json:"public"`
	}
	json.Unmarshal([]byte(out["manifest"].(string)), &manifest)
	if manifest.RedirectURL != base+"/github/created" || manifest.CallbackURLs[0] != base+"/github/authorized" ||
		manifest.SetupURL != base+"/github/installed" || manifest.Public {
		t.Errorf("manifest = %+v", manifest)
	}
	state := query(t, out["url"].(string), "state")
	if !strings.HasPrefix(out["url"].(string), gh.URL+"/settings/apps/new?") {
		t.Errorf("manifest goes to %s", out["url"])
	}

	// GitHub sends the browser back to the UI, which completes the step.
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).
		Get(srv.URL + "/github/created?code=manifest-code&state=" + state)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); resp.StatusCode != http.StatusFound || loc != "/?code=manifest-code&github=created&state="+state {
		t.Errorf("return: %d to %q", resp.StatusCode, loc)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/github/app/created", `{"code":"manifest-code","state":"forged"}`); code != 400 {
		t.Errorf("forged state: %d", code)
	}
	code, out = do(t, "POST", srv.URL+"/api/github/app/created", `{"code":"manifest-code","state":"`+state+`"}`)
	if code != 200 {
		t.Fatalf("created: %d %v", code, out)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/github/app/created", `{"code":"manifest-code","state":"`+state+`"}`); code != 400 {
		t.Errorf("reused state: %d", code)
	}

	// Next: authorization, which continues to installation while the app
	// is installed nowhere the person can reach.
	next := out["next"].(string)
	if query(t, next, "client_id") != gh.ClientID || query(t, next, "redirect_uri") != base+"/github/authorized" {
		t.Errorf("authorize at %s", next)
	}
	code, out = do(t, "POST", srv.URL+"/api/github/account", `{"code":"oauth-code","state":"`+query(t, next, "state")+`"}`)
	if code != 200 || out["next"] != gh.URL+"/apps/beethoven-test/installations/new" {
		t.Fatalf("connect: %d %v", code, out)
	}

	gh.Install("acme/app", 1, true)
	code, out = do(t, "GET", srv.URL+"/api/github", "")
	if code != 200 || out["account"].(map[string]any)["login"] != "octo" || len(out["installations"].([]any)) != 1 {
		t.Errorf("status: %d %v", code, out)
	}
	if code, info := do(t, "GET", srv.URL+"/api/info", ""); !info["github"].(bool) {
		t.Errorf("info: %d %v", code, info)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/github/app", `{"base_url":"`+base+`"}`); code != http.StatusConflict {
		t.Errorf("second app: %d", code)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/github/authorize", `{"base_url":"javascript:alert(1)"}`); code != 400 {
		t.Errorf("bad base_url: %d", code)
	}
}

func query(t *testing.T, raw, key string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}
