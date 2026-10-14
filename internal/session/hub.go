package session

import (
	"sync"

	"github.com/brdsmth/beethoven/internal/store"
)

// Event types on a session's timeline, in addition to the agent's own
// (agent.Text, agent.Delta, agent.Thinking, agent.ToolCall, agent.ToolResult).
const (
	EvSession  = "session"      // ephemeral: the session's current state (store.Session)
	EvDeleted  = "deleted"      // ephemeral: {id}
	EvPrompt   = "prompt"       // a prompt was queued (store.Prompt)
	EvStart    = "prompt.start" // {id}
	EvEnd      = "prompt.end"   // {id, status, error, cost_usd, duration_ms}
	EvSandbox  = "sandbox"      // {state, phase, message}
	EvLog      = "log"          // ephemeral: a line of sandbox boot output {text}
	EvArtifact = "artifact"     // {kind, url, title}
	EvChild    = "child"        // a child session started or finished a prompt {id, title, status, error}
	EvWarning  = "warning"      // {message}
)

// AllSessions is the hub key whose subscribers see every session's EvSession
// and EvDeleted events, for live session lists.
const AllSessions = "*"

// Hub fans events out to live subscribers. It never blocks: a subscriber
// that falls behind is dropped (its channel closed) and is expected to
// reconnect and catch up from the stored log.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[*Sub]struct{}
}

type Sub struct {
	C   <-chan store.Event
	c   chan store.Event
	key string
}

func NewHub() *Hub { return &Hub{subs: map[string]map[*Sub]struct{}{}} }

func (h *Hub) Subscribe(key string) *Sub {
	c := make(chan store.Event, 512)
	s := &Sub{C: c, c: c, key: key}
	h.mu.Lock()
	if h.subs[key] == nil {
		h.subs[key] = map[*Sub]struct{}{}
	}
	h.subs[key][s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *Hub) Unsubscribe(s *Sub) {
	h.mu.Lock()
	h.dropLocked(s)
	h.mu.Unlock()
}

func (h *Hub) Publish(key string, e store.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs[key] {
		select {
		case s.c <- e:
		default:
			h.dropLocked(s)
		}
	}
}

func (h *Hub) dropLocked(s *Sub) {
	if _, ok := h.subs[s.key][s]; !ok {
		return
	}
	delete(h.subs[s.key], s)
	if len(h.subs[s.key]) == 0 {
		delete(h.subs, s.key)
	}
	close(s.c)
}
