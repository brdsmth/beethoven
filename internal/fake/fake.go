// Package fake provides in-memory stand-ins for the sandbox provider and the
// git host, so the session machinery can be tested without Docker or GitHub.
package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/brdsmth/beethoven/internal/sandbox"
	"github.com/brdsmth/beethoven/internal/session"
)

// Provider is an in-memory sandbox.Provider whose agent behaves like Claude
// Code's stream-json mode. Prompt keywords drive it: "block" waits until the
// turn is cancelled, "fail" ends the turn with an error.
type Provider struct {
	mu      sync.Mutex
	running map[string]bool
	convos  map[string]int // conversation id -> turns, per Provider (like files in a sandbox)
	calls   []string
	Turns   []sandbox.Cmd  // every agent invocation, in order
	Boots   []sandbox.Cmd  // every boot script run
	Specs   []sandbox.Spec // every sandbox started
	Pushes  []sandbox.Cmd
	BootErr bool // make boot fail
}

func NewProvider() *Provider {
	return &Provider{running: map[string]bool{}, convos: map[string]int{}}
}

func (p *Provider) Calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

// ForgetConversations simulates a sandbox whose agent state was lost.
func (p *Provider) ForgetConversations() {
	p.mu.Lock()
	p.convos = map[string]int{}
	p.mu.Unlock()
}

func (p *Provider) Running(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running[id]
}

func (p *Provider) record(s string) {
	p.mu.Lock()
	p.calls = append(p.calls, s)
	p.mu.Unlock()
}

func (p *Provider) Start(_ context.Context, s sandbox.Spec) (string, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running[s.Name] {
		return s.Name, false, nil
	}
	p.calls = append(p.calls, "start "+s.Name)
	p.Specs = append(p.Specs, s)
	p.running[s.Name] = true
	return s.Name, true, nil
}

func (p *Provider) Stop(_ context.Context, id string) error {
	p.record("stop " + id)
	p.mu.Lock()
	p.running[id] = false
	p.mu.Unlock()
	return nil
}

func (p *Provider) Destroy(_ context.Context, id string) error {
	p.record("destroy " + id)
	p.mu.Lock()
	delete(p.running, id)
	p.mu.Unlock()
	return nil
}

func (p *Provider) Exec(ctx context.Context, id string, c sandbox.Cmd) (int, error) {
	if !p.Running(id) {
		return -1, fmt.Errorf("sandbox %s is not running", id)
	}
	switch {
	case len(c.Args) == 3 && c.Args[0] == "sh" && strings.Contains(c.Args[2], "::phase"):
		p.record("boot " + id)
		p.mu.Lock()
		p.Boots = append(p.Boots, c)
		p.mu.Unlock()
		if p.BootErr {
			fmt.Fprintln(c.Stdout, "::phase clone\nfatal: repository not found")
			return 128, nil
		}
		fmt.Fprintln(c.Stdout, "::phase clone\ncloning…\n::phase ready")
		return 0, nil
	case len(c.Args) == 3 && c.Args[0] == "sh" && strings.Contains(c.Args[2], "push --force"):
		p.mu.Lock()
		p.Pushes = append(p.Pushes, c)
		p.mu.Unlock()
		return 0, nil
	}
	p.mu.Lock()
	p.Turns = append(p.Turns, c)
	p.mu.Unlock()
	return p.claude(ctx, c)
}

func (p *Provider) claude(ctx context.Context, c sandbox.Cmd) (int, error) {
	prompt, _ := io.ReadAll(c.Stdin)
	resume := argAfter(c.Args, "--resume")
	p.mu.Lock()
	if resume != "" && p.convos[resume] == 0 {
		p.mu.Unlock()
		fmt.Fprintf(c.Stderr, "No conversation found with session ID: %s\n", resume)
		return 1, nil
	}
	sid := resume
	if sid == "" {
		sid = fmt.Sprintf("convo-%d", len(p.convos)+1)
	}
	p.convos[sid]++
	turn := p.convos[sid]
	p.mu.Unlock()

	out := func(v any) { b, _ := json.Marshal(v); c.Stdout.Write(append(b, '\n')) }
	msg := func(typ string, content ...any) {
		out(map[string]any{"type": typ, "session_id": sid, "parent_tool_use_id": nil, "message": map[string]any{"content": content}})
	}
	out(map[string]any{"type": "system", "subtype": "init", "session_id": sid})
	out(map[string]any{"type": "stream_event", "parent_tool_use_id": nil, "event": map[string]any{
		"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": "turn"}}})
	msg("assistant", map[string]any{"type": "text", "text": fmt.Sprintf("turn %d: %s", turn, prompt)})
	msg("assistant", map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": "ls"}})
	msg("user", map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "README.md"})
	switch {
	case strings.Contains(string(prompt), "block"):
		<-ctx.Done()
		return -1, ctx.Err()
	case strings.Contains(string(prompt), "fail"):
		out(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "result": "simulated failure", "session_id": sid})
		return 1, nil
	}
	out(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": fmt.Sprintf("finished turn %d", turn),
		"session_id": sid, "total_cost_usd": 0.25, "num_turns": 1})
	return 0, nil
}

func argAfter(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

// SCM is an in-memory git host.
type SCM struct {
	mu  sync.Mutex
	PRs []session.PullRequest
	Err error
}

func (s *SCM) CloneURL(repo string) string                           { return "file:///remote/" + repo + ".git" }
func (s *SCM) Token(context.Context) (string, error)                 { return "test-token", nil }
func (s *SCM) DefaultBranch(context.Context, string) (string, error) { return "main", nil }

func (s *SCM) OpenPR(_ context.Context, pr session.PullRequest) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return "", s.Err
	}
	s.PRs = append(s.PRs, pr)
	return "https://github.com/" + pr.Repo + "/pull/" + fmt.Sprint(len(s.PRs)), nil
}
