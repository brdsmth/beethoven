package slack_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/slack"
	"github.com/brdsmth/beethoven/internal/store"
)

var ctx = context.Background()

const base = "https://beethoven.test"

type harness struct {
	env   *fake.Env
	slack *fake.Slack
	c     *slack.Client
	url   string
}

// setup creates the app from its manifest, installs it in the fake
// workspace and serves the bot.
func setup(t *testing.T) *harness {
	t.Helper()
	env := fake.NewEnv(t)
	fs := fake.NewSlack(t)
	c := &slack.Client{API: fs.URL + "/api", Web: fs.URL, DB: env.DB}
	if _, err := c.CreateApp(ctx, "xoxe.xoxp-config", base); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Install(ctx, "slack-code", base+"/slack/installed", base); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := slack.NewBot(c, env.M, func(context.Context) ([]string, error) { return []string{"acme/app", "acme/web"}, nil }, log)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /slack/events", bot.Events)
	mux.HandleFunc("POST /slack/interactions", bot.Interactions)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &harness{env: env, slack: fs, c: c, url: srv.URL}
}

// post sends a request to the bot, signed with secret at a time.
func (h *harness) post(t *testing.T, path string, body []byte, secret string, at time.Time) int {
	t.Helper()
	req, _ := http.NewRequest("POST", h.url+path, bytes.NewReader(body))
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + ts + ":" + string(body)))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (h *harness) event(t *testing.T, eventID string, ev map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"type": "event_callback", "team_id": "T1", "api_app_id": "A1", "event_id": eventID, "event": ev})
	if code := h.post(t, "/slack/events", body, "ssecret", time.Now()); code != 200 {
		t.Fatalf("event: %d", code)
	}
}

func mention(user, text, ts, thread string) map[string]any {
	return map[string]any{"type": "app_mention", "user": user, "text": text, "ts": ts, "thread_ts": thread, "channel": "C1"}
}

func (h *harness) click(t *testing.T, payload map[string]any) {
	t.Helper()
	payload["type"], payload["api_app_id"], payload["team"] = "block_actions", "A1", map[string]string{"id": "T1"}
	j, _ := json.Marshal(payload)
	if code := h.post(t, "/slack/interactions", []byte(url.Values{"payload": {string(j)}}.Encode()), "ssecret", time.Now()); code != 200 {
		t.Fatalf("interaction: %d", code)
	}
}

// session waits for the session of a thread and its prompts to settle.
func (h *harness) session(t *testing.T, threadTS string, prompts int) store.Session {
	t.Helper()
	var s store.Session
	fake.WaitFor(t, "the thread's session", func() bool {
		var err error
		s, err = h.env.DB.SessionByKey(ctx, "slack:T1:C1:"+threadTS)
		if err != nil {
			return false
		}
		ps, _ := h.env.DB.Prompts(ctx, s.ID)
		return len(ps) == prompts && !slices.ContainsFunc(ps, func(p store.Prompt) bool { return p.Status == store.Queued || p.Status == store.Running })
	})
	return s
}

func (h *harness) waitCall(t *testing.T, method string, match func(url.Values) bool) url.Values {
	t.Helper()
	var found url.Values
	fake.WaitFor(t, method, func() bool {
		for _, c := range h.slack.Calls(method) {
			if match(c.Args) {
				found = c.Args
				return true
			}
		}
		return false
	})
	return found
}

func TestCreateAndInstall(t *testing.T) {
	h := setup(t)
	create := h.slack.Calls("apps.manifest.create")[0].Args.Get("manifest")
	for _, want := range []string{base + "/slack/events", base + "/slack/interactions", base + "/slack/installed", "app_mention", "users:read.email"} {
		if !strings.Contains(create, want) {
			t.Errorf("manifest lacks %q: %s", want, create)
		}
	}
	app, source := h.c.App(ctx)
	if app == nil || app.SigningSecret != "ssecret" || source != "settings" {
		t.Fatalf("app %+v from %q", app, source)
	}
	u, _ := url.Parse(h.c.InstallURL(app, base+"/slack/installed", "st"))
	if u.Query().Get("client_id") != "1.2" || !strings.Contains(u.Query().Get("scope"), "app_mentions:read") {
		t.Errorf("install URL %s", u)
	}
	st, err := h.c.Status(ctx)
	if err != nil || len(st.Installations) != 1 || st.Installations[0].TeamName != "Acme" || st.Installations[0].BaseURL != base {
		t.Errorf("status %+v, %v", st, err)
	}
	if err := h.c.Uninstall(ctx, "T1"); err != nil || len(h.slack.Calls("auth.revoke")) != 1 {
		t.Errorf("uninstall: %v", err)
	}
}

func TestMentionStartsASessionAndThreadContinuesIt(t *testing.T) {
	h := setup(t)
	h.event(t, "Ev1", mention("U1", "<@UBOT> fix the login bug in <https://github.com/acme/app|acme/app>", "100.000001", ""))

	s := h.session(t, "100.000001", 1)
	if names := store.RepoNames(s.Repos); len(names) != 1 || names[0] != "acme/app" || s.Source != "slack" {
		t.Fatalf("session = %+v", s)
	}
	if s.Author.Email != "jane@example.com" || s.Author.Name != "Jane Dev" {
		t.Errorf("author = %+v, want the Slack user", s.Author)
	}
	status := h.waitCall(t, "chat.postMessage", func(a url.Values) bool { return strings.HasPrefix(a.Get("text"), "Working on it") })
	if status.Get("thread_ts") != "100.000001" || !strings.Contains(status.Get("blocks"), base+"/#/s/"+s.ID) ||
		!strings.Contains(status.Get("text"), "acme/app") {
		t.Errorf("status reply = %v", status)
	}
	result := h.waitCall(t, "chat.postMessage", func(a url.Values) bool { return strings.Contains(a.Get("blocks"), "finished turn 1") })
	if result.Get("thread_ts") != "100.000001" {
		t.Errorf("result in thread %q", result.Get("thread_ts"))
	}
	h.waitCall(t, "reactions.add", func(a url.Values) bool { return a.Get("name") == "white_check_mark" })
	h.waitCall(t, "chat.update", func(a url.Values) bool { return strings.HasPrefix(a.Get("text"), "Done") })

	// A mention in the thread is a follow-up, with what was said since.
	h.slack.SetReplies(
		map[string]any{"ts": "100.000001", "user": "U1", "text": "<@UBOT> fix the login bug in acme/app"},
		map[string]any{"ts": "100.000003", "user": "U2", "text": "it breaks on Safari &amp; Firefox"},
		map[string]any{"ts": "100.000004", "bot_id": "B1", "text": "Working on it"},
		map[string]any{"ts": "100.000005", "user": "U1", "text": "<@UBOT> also add a test"},
	)
	h.event(t, "Ev2", mention("U1", "<@UBOT> also add a test", "100.000005", "100.000001"))
	s = h.session(t, "100.000001", 2)
	ps, _ := h.env.DB.Prompts(ctx, s.ID)
	got := ps[1].Content
	if !strings.HasPrefix(got, "also add a test") || !strings.Contains(got, "[bob]: it breaks on Safari & Firefox") ||
		strings.Contains(got, "fix the login bug") || strings.Contains(got, "Working on it") {
		t.Errorf("follow-up prompt = %q", got)
	}
}

func TestOnlySignedFreshEventsCountOnce(t *testing.T) {
	h := setup(t)
	verification := []byte(`{"type":"url_verification","challenge":"abc"}`)
	resp, err := http.Post(h.url+"/slack/events", "application/json", bytes.NewReader(verification))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(resp.Body); string(b) != "abc" {
		t.Errorf("challenge answered %q", b)
	}
	body, _ := json.Marshal(map[string]any{"type": "event_callback", "team_id": "T1", "api_app_id": "A1", "event_id": "Ev9",
		"event": mention("U1", "<@UBOT> look at acme/web", "300.000001", "")})
	if code := h.post(t, "/slack/events", body, "wrong", time.Now()); code != http.StatusUnauthorized {
		t.Errorf("bad signature: %d", code)
	}
	if code := h.post(t, "/slack/events", body, "ssecret", time.Now().Add(-10*time.Minute)); code != http.StatusUnauthorized {
		t.Errorf("replayed: %d", code)
	}
	for range 3 { // Slack redelivers
		h.post(t, "/slack/events", body, "ssecret", time.Now())
	}
	s := h.session(t, "300.000001", 1)
	time.Sleep(100 * time.Millisecond)
	if ps, _ := h.env.DB.Prompts(ctx, s.ID); len(ps) != 1 {
		t.Errorf("%d prompts for one event", len(ps))
	}
}

func TestPickerRemembersTheChannel(t *testing.T) {
	h := setup(t)
	h.event(t, "Ev1", mention("U1", "<@UBOT> tidy up the README", "400.000001", ""))
	picker := h.waitCall(t, "chat.postMessage", func(a url.Values) bool { return strings.Contains(a.Get("blocks"), "static_select") })
	if !strings.Contains(picker.Get("blocks"), "repo:acme/web") {
		t.Fatalf("picker = %s", picker.Get("blocks"))
	}
	var blocks []struct {
		BlockID string `json:"block_id"`
	}
	json.Unmarshal([]byte(picker.Get("blocks")), &blocks)
	choose := func(user string) {
		h.click(t, map[string]any{
			"user": map[string]string{"id": user}, "channel": map[string]string{"id": "C1"},
			"message": map[string]string{"ts": "900.000001", "thread_ts": "400.000001"},
			"state":   map[string]any{"values": map[string]any{"remember": map[string]any{"remember": map[string]any{"selected_options": []any{map[string]string{"value": "yes"}}}}}},
			"actions": []any{map[string]any{"action_id": "pick", "block_id": blocks[1].BlockID, "selected_option": map[string]string{"value": "repo:acme/web"}}},
		})
	}
	choose("U2") // not theirs to answer
	h.waitCall(t, "chat.postEphemeral", func(a url.Values) bool { return a.Get("user") == "U2" && strings.Contains(a.Get("text"), "Only <@U1>") })
	choose("U1")
	if s := h.session(t, "400.000001", 1); store.RepoNames(s.Repos)[0] != "acme/web" {
		t.Errorf("worked on %v", s.Repos)
	}
	if c, err := h.env.DB.SlackChannel(ctx, "T1", "C1"); err != nil || c.Repos[0].Repo != "acme/web" || c.Name != "#eng" {
		t.Errorf("channel default = %+v, %v", c, err)
	}

	before := len(h.slack.Calls("chat.postMessage"))
	h.event(t, "Ev2", mention("U2", "<@UBOT> bump the version", "401.000001", ""))
	if s := h.session(t, "401.000001", 1); store.RepoNames(s.Repos)[0] != "acme/web" {
		t.Errorf("the channel default didn't apply: %v", s.Repos)
	}
	for _, c := range h.slack.Calls("chat.postMessage")[before:] {
		if strings.Contains(c.Args.Get("blocks"), "static_select") {
			t.Error("asked again in a channel with a default")
		}
	}
}

func TestGuestsCantStartSessions(t *testing.T) {
	h := setup(t)
	h.slack.SetUser("U9", map[string]any{"team_id": "T1", "is_restricted": true, "real_name": "Guest"})
	h.slack.SetUser("U8", map[string]any{"team_id": "T2", "real_name": "Partner"}) // via a shared channel
	h.event(t, "Ev1", mention("U9", "<@UBOT> fix acme/app", "500.000001", ""))
	h.event(t, "Ev2", mention("U8", "<@UBOT> fix acme/app", "500.000002", ""))
	h.waitCall(t, "chat.postEphemeral", func(a url.Values) bool { return a.Get("user") == "U9" && strings.Contains(a.Get("text"), "Guests") })
	h.waitCall(t, "chat.postEphemeral", func(a url.Values) bool { return a.Get("user") == "U8" && strings.Contains(a.Get("text"), "Acme") })
	if list, _ := h.env.DB.Sessions(ctx, store.SessionFilter{}); len(list) != 0 {
		t.Errorf("%d sessions", len(list))
	}
}

func TestStopButton(t *testing.T) {
	h := setup(t)
	h.event(t, "Ev1", mention("U1", "<@UBOT> block on acme/app", "600.000001", ""))
	var s store.Session
	fake.WaitFor(t, "a running session", func() bool {
		var err error
		s, err = h.env.DB.SessionByKey(ctx, "slack:T1:C1:600.000001")
		return err == nil && s.Status == store.Running
	})
	h.waitCall(t, "chat.postMessage", func(a url.Values) bool { return strings.Contains(a.Get("blocks"), `"action_id":"stop"`) })
	h.click(t, map[string]any{
		"user": map[string]string{"id": "U2"}, "channel": map[string]string{"id": "C1"},
		"message": map[string]string{"ts": "900.000001", "thread_ts": "600.000001"},
		"actions": []any{map[string]any{"action_id": "stop", "value": s.ID}},
	})
	h.waitCall(t, "reactions.add", func(a url.Values) bool { return a.Get("name") == "black_square_for_stop" })
	h.waitCall(t, "chat.update", func(a url.Values) bool { return strings.HasPrefix(a.Get("text"), "Stopped") })
}

func TestFilesAreAttached(t *testing.T) {
	h := setup(t)
	png := []byte("\x89PNG\r\n\x1a\nscreenshot")
	h.slack.SetFile("F1", "screenshot.png", "image/png", png)
	h.slack.SetFile("F2", "build.log", "text/plain", []byte("error: boom"))
	h.slack.SetFile("F3", "old.txt", "text/plain", []byte("before"))
	h.slack.SetReplies(
		map[string]any{"ts": "700.000001", "user": "U2", "text": "the build is broken", "subtype": "file_share",
			"files": []any{map[string]any{"id": "F2", "name": "build.log"}}},
		map[string]any{"ts": "700.000002", "user": "U1", "text": "<@UBOT> fix acme/app", "files": []any{map[string]any{"id": "F1", "name": "screenshot.png"}}},
	)
	ev := mention("U1", "<@UBOT> fix acme/app", "700.000002", "700.000001")
	ev["files"] = []any{map[string]any{"id": "F1", "name": "screenshot.png"}, map[string]any{"id": "F4", "name": "doc"}}
	h.event(t, "Ev1", ev)

	s := h.session(t, "700.000001", 1)
	ps, _ := h.env.DB.Prompts(ctx, s.ID)
	p := ps[0]
	if a := p.Attachments; len(a) != 2 || a[0].Name != "screenshot.png" || a[0].ContentType != "image/png" || a[1].Name != "build.log" {
		t.Fatalf("attachments = %+v, want the request's screenshot, then the thread's log", a)
	}
	files, _ := h.env.DB.PromptFiles(ctx, p.ID)
	if string(files[0].Data) != string(png) || string(files[1].Data) != "error: boom" {
		t.Errorf("contents = %q, %q", files[0].Data, files[1].Data)
	}
	for _, want := range []string{"[bob]: the build is broken (shared build.log)", "couldn't be attached: doc (Slack couldn't find it)"} {
		if !strings.Contains(p.Content, want) {
			t.Errorf("prompt lacks %q: %q", want, p.Content)
		}
	}
	h.waitCall(t, "chat.postMessage", func(a url.Values) bool { return strings.HasPrefix(a.Get("text"), ":warning: I left out doc") })
}

func TestADirectMessageCanBeJustAFile(t *testing.T) {
	h := setup(t)
	h.slack.SetFile("F1", "trace.txt", "text/plain", []byte("panic: nil map"))
	if err := h.env.DB.SaveSlackChannel(ctx, &store.SlackChannel{TeamID: "T1", Channel: "D1", Repos: []store.RepoRef{{Repo: "acme/app"}}}); err != nil {
		t.Fatal(err)
	}
	h.event(t, "Ev1", map[string]any{"type": "message", "subtype": "file_share", "channel_type": "im", "channel": "D1",
		"user": "U1", "text": "", "ts": "800.000001", "files": []any{map[string]any{"id": "F1", "name": "trace.txt"}}})
	var s store.Session
	fake.WaitFor(t, "the session", func() bool {
		var err error
		s, err = h.env.DB.SessionByKey(ctx, "slack:T1:D1:800.000001")
		return err == nil
	})
	ps, _ := h.env.DB.Prompts(ctx, s.ID)
	if len(ps) != 1 || ps[0].Content != "" || len(ps[0].Attachments) != 1 || s.Title != "trace.txt" {
		t.Errorf("session %q, prompts %+v", s.Title, ps)
	}
}

func TestFilesWithoutTheScope(t *testing.T) {
	h := setup(t)
	h.slack.NoFileScope = true
	h.slack.SetFile("F1", "screenshot.png", "image/png", []byte("\x89PNG\r\n\x1a\n"))
	ev := mention("U1", "<@UBOT> what's wrong in acme/app?", "900.000001", "")
	ev["files"] = []any{map[string]any{"id": "F1", "name": "screenshot.png"}}
	h.event(t, "Ev1", ev)

	s := h.session(t, "900.000001", 1)
	ps, _ := h.env.DB.Prompts(ctx, s.ID)
	if len(ps[0].Attachments) != 0 || !strings.Contains(ps[0].Content, "screenshot.png (the Slack app can't read files)") {
		t.Errorf("prompt = %+v", ps[0])
	}
	h.waitCall(t, "chat.postMessage", func(a url.Values) bool {
		return strings.Contains(a.Get("text"), "`files:read`") && strings.Contains(a.Get("text"), "https://api.slack.com/apps/A1/oauth")
	})
}
