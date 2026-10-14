package tools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
	"github.com/brdsmth/beethoven/internal/tools"
)

var ctx = context.Background()

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func connect(t *testing.T, url, token string) (*mcp.ClientSession, error) {
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	return c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: url, HTTPClient: &http.Client{Transport: bearer{token}}, MaxRetries: -1}, nil)
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestTools(t *testing.T) {
	env := fake.NewEnv(t)
	srv := httptest.NewServer(tools.Handler(env.M))
	defer srv.Close()

	parent, p, err := env.M.Submit(ctx, session.Request{Prompt: "parent", Repos: []store.RepoRef{{Repo: "acme/app"}}, Source: "test",
		Author: store.Author{Name: "Jane", Email: "jane@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connect(t, srv.URL, "bogus.token"); err == nil {
		t.Fatal("connected with a bad token")
	}
	cs, err := connect(t, srv.URL, env.M.Token(parent.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if want := []string{"create_pull_request", "get_session", "list_sessions", "send_prompt", "spawn_session", "stop_session"}; !slices.Equal(names, want) {
		t.Errorf("tools = %v", names)
	}

	out, isErr := call(t, cs, "spawn_session", map[string]any{"prompt": "do the child part", "title": "child"})
	childID := regexp.MustCompile(`child session (\w+)`).FindStringSubmatch(out)
	if isErr || childID == nil {
		t.Fatalf("spawn_session = %q (error=%v)", out, isErr)
	}
	child, _ := env.M.Session(ctx, childID[1])
	if child.ParentID != parent.ID || child.Repos[0].Repo != "acme/app" || child.Author.Name != "Jane" {
		t.Errorf("child = %+v", child)
	}

	out, _ = call(t, cs, "get_session", map[string]any{"session_id": child.ID, "wait_seconds": 5})
	var got struct {
		Status  string
		Prompts []struct{ Status, Result string } `json:"recent_prompts"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("get_session output %q: %v", out, err)
	}
	if got.Status != "done" || len(got.Prompts) != 1 || got.Prompts[0].Result != "finished turn 1" {
		t.Errorf("get_session = %+v", got)
	}

	if out, _ := call(t, cs, "list_sessions", nil); !strings.Contains(out, child.ID) {
		t.Errorf("list_sessions = %q", out)
	}
	if out, isErr := call(t, cs, "send_prompt", map[string]any{"session_id": child.ID, "prompt": "more"}); isErr {
		t.Errorf("send_prompt = %q", out)
	}

	// Only direct children are reachable.
	stranger, _, _ := env.M.Submit(ctx, session.Request{Prompt: "unrelated", Source: "test"})
	for _, tool := range []string{"get_session", "send_prompt", "stop_session"} {
		if out, isErr := call(t, cs, tool, map[string]any{"session_id": stranger.ID, "prompt": "x"}); !isErr {
			t.Errorf("%s on a non-child succeeded: %q", tool, out)
		}
	}

	fake.WaitFor(t, "parent prompt done", func() bool { q, _ := env.DB.Prompt(ctx, p.ID); return q.Status == store.Done })
	out, isErr = call(t, cs, "create_pull_request", map[string]any{"title": "Add things", "body": "Body"})
	if isErr || !strings.Contains(out, "https://github.com/acme/app/pull/1") {
		t.Errorf("create_pull_request = %q (error=%v)", out, isErr)
	}
}
