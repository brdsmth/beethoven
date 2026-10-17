package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/brdsmth/beethoven/internal/fake"
)

func TestCapabilities(t *testing.T) {
	ctx := context.Background()
	srv, env := server(t, "")

	resp, err := http.Get(srv.URL + "/api/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		ID      string
		Install string
		Secrets []struct{ Name string }
	}
	var raw []map[string]any
	json.NewDecoder(resp.Body).Decode(&raw)
	resp.Body.Close()
	for _, c := range raw {
		if _, ok := c["secrets"].([]any); !ok {
			t.Errorf("%v's secrets are %v, want a list", c["id"], c["secrets"])
		}
	}
	var catalog []entry
	b, _ := json.Marshal(raw)
	json.Unmarshal(b, &catalog)
	i := slices.IndexFunc(catalog, func(c entry) bool { return c.ID == "expo" })
	if i < 0 || !strings.Contains(catalog[i].Install, "eas-cli") || catalog[i].Secrets[0].Name != "EXPO_TOKEN" {
		t.Fatalf("catalog = %+v", catalog)
	}

	// Settings and environments only take capabilities from the catalog.
	if code, out := do(t, "PUT", srv.URL+"/api/settings", `{"capabilities":["go","cobol"]}`); code != 400 || !strings.Contains(out["error"].(string), "cobol") {
		t.Errorf("unknown global capability: %d %v", code, out)
	}
	if code, out := do(t, "PUT", srv.URL+"/api/settings", `{"capabilities":["go"]}`); code != 200 {
		t.Errorf("global capability: %d %v", code, out)
	}
	code, out := do(t, "POST", srv.URL+"/api/environments", `{"name":"mobile","overrides":{"capabilities":["expo","android"]}}`)
	if code != 201 {
		t.Fatalf("environment: %d %v", code, out)
	}
	e, _ := env.DB.Environment(ctx, out["id"].(string))
	if !slices.Equal(e.Overrides.Capabilities, []string{"expo", "android"}) {
		t.Errorf("environment capabilities = %v", e.Overrides.Capabilities)
	}

	// Added to a session from the UI.
	code, out = do(t, "POST", srv.URL+"/api/sessions", `{"prompt":"hi","repos":[{"repo":"acme/app"}]}`)
	if code != 201 {
		t.Fatalf("session: %d %v", code, out)
	}
	id := out["session"].(map[string]any)["id"].(string)
	fake.WaitFor(t, "session done", func() bool { s, _ := env.M.Session(ctx, id); return s.Status == "done" })
	if code, out := do(t, "POST", srv.URL+"/api/sessions/"+id+"/capabilities", `{"id":"rust"}`); code != 200 || out["kept"] != "repository" {
		t.Errorf("add: %d %v", code, out)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/sessions/"+id+"/capabilities", `{"id":"cobol"}`); code != 400 {
		t.Errorf("unknown: %d", code)
	}
	if evs, _ := env.DB.EventsOfType(ctx, id, "capability"); len(evs) != 1 || !strings.Contains(string(evs[0].Data), `"by":"web"`) {
		t.Errorf("events = %v", evs)
	}
}
