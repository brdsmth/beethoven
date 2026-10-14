package api_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/brdsmth/beethoven/internal/api"
)

func body(t *testing.T, method, url, payload string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestSettingsAPI(t *testing.T) {
	srv, _ := server(t, "")
	for _, bad := range []string{`{"effort":"extreme"}`, `{"author_name":"Only Name"}`, `{"memory_mb":12}`, `{"branch_prefix":"a..b"}`, `{"unknown":1}`} {
		if code, out := body(t, "PUT", srv.URL+"/api/settings", bad); code != http.StatusBadRequest {
			t.Errorf("PUT %s: %d %s", bad, code, out)
		}
	}
	if code, out := body(t, "PUT", srv.URL+"/api/settings", `{"model":"opus","effort":"high","instructions":"Be terse.","draft_prs":true,"author_name":"Ada","author_email":"ada@example.com"}`); code != 200 {
		t.Fatalf("PUT: %d %s", code, out)
	}
	_, out := body(t, "GET", srv.URL+"/api/settings", "")
	if !strings.Contains(out, `"model":"opus"`) || !strings.Contains(out, `"defaults"`) {
		t.Errorf("GET = %s", out)
	}
	if _, out := body(t, "GET", srv.URL+"/api/info", ""); !strings.Contains(out, `"author":{"name":"Ada","email":"ada@example.com"}`) || !strings.Contains(out, `"model":"opus"`) {
		t.Errorf("info = %s", out)
	}
}

func TestCredentialsAPI(t *testing.T) {
	srv, _ := server(t, "")
	if code, _ := body(t, "PUT", srv.URL+"/api/credentials", `{"name":"PATH","value":"x"}`); code != http.StatusBadRequest {
		t.Errorf("unknown credential: %d", code)
	}
	if code, _ := body(t, "PUT", srv.URL+"/api/credentials", `{"name":"ANTHROPIC_API_KEY","value":"sk-ant-secret"}`); code != http.StatusNoContent {
		t.Errorf("put: %d", code)
	}
	_, out := body(t, "GET", srv.URL+"/api/credentials", "")
	if strings.Contains(out, "sk-ant-secret") || !strings.Contains(out, `"ANTHROPIC_API_KEY":true`) || !strings.Contains(out, `"anthropic":{"source":"settings"}`) {
		t.Errorf("credentials = %s", out)
	}
	if _, out := body(t, "GET", srv.URL+"/api/info", ""); !strings.Contains(out, `"anthropic":true`) {
		t.Errorf("info = %s", out)
	}
	if _, out := body(t, "GET", srv.URL+"/api/secrets", ""); strings.Contains(out, "ANTHROPIC") {
		t.Errorf("credential listed as a secret: %s", out)
	}
	body(t, "PUT", srv.URL+"/api/credentials", `{"name":"ANTHROPIC_API_KEY","value":""}`)
	if _, out := body(t, "GET", srv.URL+"/api/credentials", ""); !strings.Contains(out, `"ANTHROPIC_API_KEY":false`) {
		t.Errorf("after removal = %s", out)
	}
}

func TestEnvironmentsAPI(t *testing.T) {
	srv, _ := server(t, "")
	env := `{"name":"Web stack","description":"d","repos":[{"repo":"acme/api","base_branch":"main"},{"repo":"acme/web","base_branch":""}],
		"setup_script":"make deps","start_script":"","inherit_secrets":false,"overrides":{"model":"opus"}}`
	code, out := body(t, "POST", srv.URL+"/api/environments", env)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, out)
	}
	id := between(out, `"id":"`, `"`)
	if code, out := body(t, "POST", srv.URL+"/api/environments", strings.Replace(env, "Web stack", "WEB STACK", 1)); code != http.StatusConflict {
		t.Errorf("duplicate name: %d %s", code, out)
	}
	if code, _ := body(t, "POST", srv.URL+"/api/environments", `{"name":"x","repos":[{"repo":"a/app","base_branch":""},{"repo":"b/app","base_branch":""}]}`); code != http.StatusBadRequest {
		t.Errorf("colliding checkouts: %d", code)
	}

	body(t, "PUT", srv.URL+"/api/secrets", `{"scope":"","name":"GLOBAL","value":"g"}`)
	if code, out := body(t, "PUT", srv.URL+"/api/secrets", `{"scope":"env:`+id+`","name":"TOKEN","value":"t"}`); code != http.StatusNoContent {
		t.Fatalf("env secret: %d %s", code, out)
	}
	if code, _ := body(t, "PUT", srv.URL+"/api/secrets", `{"scope":"env:nope","name":"TOKEN","value":"t"}`); code != http.StatusBadRequest {
		t.Errorf("unknown environment scope: %d", code)
	}
	_, out = body(t, "GET", srv.URL+"/api/secrets/effective?environment="+id, "")
	if !strings.Contains(out, `"layers":["env:`+id+`"]`) || !strings.Contains(out, `"name":"TOKEN"`) || strings.Contains(out, "GLOBAL") {
		t.Errorf("effective without inheritance = %s", out)
	}

	code, out = body(t, "POST", srv.URL+"/api/sessions", `{"prompt":"go","environment_id":"`+id+`"}`)
	if code != http.StatusCreated || !strings.Contains(out, `"repos":[{"repo":"acme/api","base_branch":"main"},{"repo":"acme/web","base_branch":""}]`) {
		t.Errorf("session from environment: %d %s", code, out)
	}
	if code, _ := body(t, "POST", srv.URL+"/api/sessions", `{"prompt":"go","environment_id":"`+id+`","repo":"acme/x"}`); code != http.StatusBadRequest {
		t.Errorf("environment and repos together: %d", code)
	}
	if code, _ := body(t, "DELETE", srv.URL+"/api/environments/"+id, ""); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	if _, out := body(t, "GET", srv.URL+"/api/secrets", ""); strings.Contains(out, "TOKEN") {
		t.Errorf("environment secrets survived its deletion: %s", out)
	}
}

func TestImportSecrets(t *testing.T) {
	srv, _ := server(t, "")
	text := "# comment\nexport API_KEY=\"abc\"\nPLAIN = value # note\nQUOTED='a b'\nPATH=/bin\nnot a line\nAPI_KEY=override\n"
	code, out := body(t, "POST", srv.URL+"/api/secrets/import", `{"scope":"acme/app","text":`+quote(text)+`}`)
	if code != 200 {
		t.Fatalf("import: %d %s", code, out)
	}
	if !strings.Contains(out, `"imported":["API_KEY","PLAIN","QUOTED"]`) || !strings.Contains(out, `"line":5`) || !strings.Contains(out, `"line":6`) {
		t.Errorf("import = %s", out)
	}
	vars, _ := api.ParseDotenv(text)
	got := map[string]string{}
	for _, v := range vars {
		got[v.Name] = v.Value
	}
	if got["API_KEY"] != "override" || got["PLAIN"] != "value" || got["QUOTED"] != "a b" {
		t.Errorf("parsed = %v", got)
	}
}

func TestMCPServersAPI(t *testing.T) {
	srv, env := server(t, "")
	code, out := body(t, "POST", srv.URL+"/api/mcp-servers", `{"name":"sentry","scope":"","type":"http","url":"https://mcp.sentry.dev/mcp","secret":{"Authorization":"Bearer one"}}`)
	if code != http.StatusCreated || strings.Contains(out, "Bearer") || !strings.Contains(out, `"secret_keys":["Authorization"]`) {
		t.Fatalf("create: %d %s", code, out)
	}
	id := between(out, `"id":"`, `"`)
	for _, bad := range []string{
		`{"name":"beethoven","scope":"","type":"http","url":"https://x"}`,
		`{"name":"x","scope":"","type":"stdio","command":[]}`,
		`{"name":"x","scope":"","type":"http","url":"ftp://x"}`,
		`{"name":"sentry","scope":"","type":"http","url":"https://x"}`,
	} {
		if code, out := body(t, "POST", srv.URL+"/api/mcp-servers", bad); code < 400 {
			t.Errorf("POST %s: %d %s", bad, code, out)
		}
	}
	// A blank value keeps the stored one; listed keys replace the set.
	code, out = body(t, "PUT", srv.URL+"/api/mcp-servers/"+id, `{"name":"sentry","scope":"","type":"http","url":"https://mcp.sentry.dev/mcp","secret":{"Authorization":"","X-Org":"acme"},"enabled":false}`)
	if code != 200 || !strings.Contains(out, `"secret_keys":["Authorization","X-Org"]`) || !strings.Contains(out, `"enabled":false`) {
		t.Errorf("update: %d %s", code, out)
	}
	if m, _ := env.DB.MCPServer(context.Background(), id); m.Secret["Authorization"] != "Bearer one" || m.Secret["X-Org"] != "acme" {
		t.Errorf("stored secret = %v", m.Secret)
	}
}

func between(s, start, end string) string {
	_, rest, _ := strings.Cut(s, start)
	v, _, _ := strings.Cut(rest, end)
	return v
}

func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}
