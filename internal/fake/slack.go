package fake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// Slack is a fake Slack: the Web API of one workspace, T1 ("Acme"), whose
// bot user is UBOT. Point a client's API at URL+"/api" and Web at URL.
type Slack struct {
	URL string

	mu      sync.Mutex
	calls   []SlackCall
	users   map[string]map[string]any
	replies []map[string]any
	ts      int
}

// SlackCall is one Web API call.
type SlackCall struct {
	Method string
	Args   url.Values
}

const SlackBotToken = "xoxb-test"

func NewSlack(t testing.TB) *Slack {
	t.Helper()
	f := &Slack{users: map[string]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	f.URL = srv.URL
	f.SetUser("U1", map[string]any{"team_id": "T1", "real_name": "Jane Dev", "profile": map[string]any{
		"real_name": "Jane Dev", "display_name": "jane", "email": "jane@example.com"}})
	f.SetUser("U2", map[string]any{"team_id": "T1", "real_name": "Bob Ops", "profile": map[string]any{
		"real_name": "Bob Ops", "display_name": "bob", "email": "bob@example.com"}})
	return f
}

// SetUser sets what users.info says about a user.
func (f *Slack) SetUser(id string, user map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	user["id"] = id
	f.users[id] = user
}

// SetReplies sets the messages conversations.replies returns.
func (f *Slack) SetReplies(msgs ...map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = msgs
}

// Calls returns the calls made to method, or every call for "".
func (f *Slack) Calls(method string) []SlackCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []SlackCall
	for _, c := range f.calls {
		if method == "" || c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *Slack) serve(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	method := strings.TrimPrefix(r.URL.Path, "/api/")
	reply := func(v map[string]any) {
		if _, ok := v["ok"]; !ok {
			v["ok"] = true
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	f.mu.Lock()
	f.calls = append(f.calls, SlackCall{Method: method, Args: r.PostForm})
	f.mu.Unlock()
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	switch method {
	case "apps.manifest.create":
		if auth != "xoxe.xoxp-config" {
			reply(map[string]any{"ok": false, "error": "invalid_auth"})
			return
		}
		var m map[string]any
		if json.Unmarshal([]byte(r.PostForm.Get("manifest")), &m) != nil {
			reply(map[string]any{"ok": false, "error": "invalid_manifest", "errors": []any{map[string]string{"message": "not JSON", "pointer": "/"}}})
			return
		}
		reply(map[string]any{"app_id": "A1", "credentials": map[string]string{"client_id": "1.2", "client_secret": "csecret",
			"verification_token": "v", "signing_secret": "ssecret"}, "oauth_authorize_url": "https://slack.com/oauth/v2/authorize?client_id=1.2"})
		return
	case "oauth.v2.access":
		if r.PostForm.Get("client_id") != "1.2" || r.PostForm.Get("client_secret") != "csecret" || r.PostForm.Get("code") != "slack-code" {
			reply(map[string]any{"ok": false, "error": "invalid_code"})
			return
		}
		reply(map[string]any{"access_token": SlackBotToken, "token_type": "bot", "bot_user_id": "UBOT", "app_id": "A1",
			"team": map[string]string{"id": "T1", "name": "Acme"}, "authed_user": map[string]string{"id": "U1"}})
		return
	}
	if auth != SlackBotToken {
		reply(map[string]any{"ok": false, "error": "invalid_auth"})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch method {
	case "users.info":
		u, ok := f.users[r.PostForm.Get("user")]
		if !ok {
			reply(map[string]any{"ok": false, "error": "user_not_found"})
			return
		}
		reply(map[string]any{"user": u})
	case "chat.postMessage":
		f.ts++
		reply(map[string]any{"ts": fmt.Sprintf("900.%06d", f.ts), "channel": r.PostForm.Get("channel")})
	case "conversations.replies":
		reply(map[string]any{"messages": f.replies})
	case "conversations.info":
		reply(map[string]any{"channel": map[string]any{"id": r.PostForm.Get("channel"), "name": "eng"}})
	default: // chat.update, chat.postEphemeral, reactions.add, reactions.remove, auth.revoke
		reply(map[string]any{})
	}
}
