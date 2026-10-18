package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brdsmth/beethoven/internal/store"
)

func TestLogs(t *testing.T) {
	ev := func(seq int64, typ string, data map[string]any) store.Event {
		b, _ := json.Marshal(data)
		return store.Event{Session: "s1", Seq: seq, Type: typ, Data: b, At: time.Date(2026, 10, 18, 9, 0, int(seq), 0, time.UTC)}
	}
	evs := []store.Event{
		ev(1, "prompt", map[string]any{"id": "p1", "content": "fix the build", "source": "web", "author": map[string]any{"name": "Jane"}}),
		ev(2, "tool", map[string]any{"name": "Bash", "input": map[string]any{"command": "go test ./..."}}),
		ev(3, "tool.result", map[string]any{"is_error": true, "output": "FAIL: TestThing\n"}),
		ev(4, "prompt.end", map[string]any{"id": "p1", "status": "failed", "error": "turn timed out"}),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sessions/s1" {
			json.NewEncoder(w).Encode(store.Session{ID: "s1", Title: "Fix it", Status: "idle"})
			return
		}
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		page := []store.Event{}
		for _, e := range evs {
			if e.Seq > after && len(page) < 3 { // pages of three, so the client must ask again
				page = append(page, e)
			}
		}
		json.NewEncoder(w).Encode(page)
	}))
	defer srv.Close()
	t.Setenv("BEETHOVEN_URL", srv.URL)

	got, err := fetchEvents("s1")
	if err != nil || len(got) != 4 {
		t.Fatalf("fetched %d events, %v", len(got), err)
	}
	var b strings.Builder
	transcript(&b, store.Session{ID: "s1", Title: "Fix it"}, got, 0)
	for _, want := range []string{"Session s1: Fix it", "prompt p1 from Jane (web)", `tool call Bash {"command":"go test ./..."}`,
		"tool ERROR:\nFAIL: TestThing", "prompt p1 ended failed ($0.00) turn timed out"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("transcript lacks %q:\n%s", want, b.String())
		}
	}
}
