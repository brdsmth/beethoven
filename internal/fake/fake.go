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
	"github.com/brdsmth/beethoven/internal/store"
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
	Files   map[string][]byte // files written into sandboxes, by path; read with File
	BootErr bool              // make boot fail
	// Capabilities: what the repositories name and suggest, which ones fail
	// to install, and every capability script run besides boot.
	RepoCapabilities []string
	Suggest          []string
	CapabilityErr    map[string]bool
	CapabilityRuns   []sandbox.Cmd
}

// capabilities answers the capability scripts: each id in CAPABILITIES
// (and, booting, those the repositories name) installs, unless it is set to
// fail, and the suggestions are printed.
func (p *Provider) capabilities(c sandbox.Cmd, boot bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := strings.Fields(c.Env["CAPABILITIES"])
	if boot {
		ids = append(ids, p.RepoCapabilities...)
	}
	for _, id := range ids {
		fmt.Fprintf(c.Stdout, "::phase capability %s\n", id)
		if p.CapabilityErr[id] {
			fmt.Fprintf(c.Stdout, "::warn the %s capability failed to install\n", id)
			continue
		}
		if id == "android" {
			fmt.Fprintln(c.Stdout, "::env ANDROID_HOME=/home/agent/android-sdk")
		}
		fmt.Fprintf(c.Stdout, "::capability %s\n", id)
		if rec := c.Env["CAP_RECORD"]; rec != "" {
			p.Files[rec] = append(p.Files[rec], []byte(id+"\n")...)
			fmt.Fprintf(c.Stdout, "::recorded %s\n", rec)
		}
	}
	for _, id := range p.Suggest {
		fmt.Fprintf(c.Stdout, "::suggest %s\n", id)
	}
}

func NewProvider() *Provider {
	return &Provider{running: map[string]bool{}, convos: map[string]int{}, Files: map[string][]byte{}}
}

// File returns a file written into a sandbox.
func (p *Provider) File(path string) ([]byte, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, ok := p.Files[path]
	return b, ok
}

// AttachmentDir is where the agent's home would hold a prompt's attachments.
func AttachmentDir(promptID string) string { return "/home/agent/attachments/" + promptID }

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
	case len(c.Args) == 3 && c.Args[0] == "sh" && (strings.HasSuffix(c.Args[2], "\ncap_add\n") || strings.HasSuffix(c.Args[2], "\ncap_detect\n")):
		p.record("capabilities " + id)
		p.mu.Lock()
		p.CapabilityRuns = append(p.CapabilityRuns, c)
		p.mu.Unlock()
		if strings.HasSuffix(c.Args[2], "\ncap_add\n") {
			p.capabilities(c, false)
		} else {
			p.mu.Lock()
			for _, s := range p.Suggest {
				fmt.Fprintf(c.Stdout, "::suggest %s\n", s)
			}
			p.mu.Unlock()
		}
		return 0, nil
	case len(c.Args) == 3 && c.Args[0] == "sh" && strings.Contains(c.Args[2], "::phase"):
		p.record("boot " + id)
		p.mu.Lock()
		p.Boots = append(p.Boots, c)
		p.mu.Unlock()
		if p.BootErr {
			fmt.Fprintln(c.Stdout, "::phase clone\nfatal: repository not found")
			return 128, nil
		}
		fmt.Fprintln(c.Stdout, "::phase clone\ncloning…")
		p.capabilities(c, true)
		fmt.Fprintln(c.Stdout, "::phase ready")
		return 0, nil
	case len(c.Args) == 3 && c.Args[0] == "sh" && strings.Contains(c.Args[2], "push --force"):
		p.mu.Lock()
		p.Pushes = append(p.Pushes, c)
		p.mu.Unlock()
		return 0, nil
	case len(c.Args) == 3 && c.Args[0] == "sh" && strings.Contains(c.Args[2], `cat "$FILE"`):
		p.mu.Lock()
		data, ok := p.Files[c.Env["FILE"]]
		p.mu.Unlock()
		if !ok {
			fmt.Fprintf(c.Stderr, "no such file: %s\n", c.Env["FILE"])
			return 3, nil
		}
		c.Stdout.Write(data)
		return 0, nil
	case len(c.Args) == 3 && c.Args[0] == "sh" && strings.Contains(c.Args[2], "/attachments/$PROMPT"):
		data, _ := io.ReadAll(c.Stdin)
		dir := AttachmentDir(c.Env["PROMPT"])
		p.mu.Lock()
		p.Files[dir+"/"+c.Env["NAME"]] = data
		p.mu.Unlock()
		fmt.Fprintln(c.Stdout, dir)
		return 0, nil
	}
	p.mu.Lock()
	p.Turns = append(p.Turns, c)
	p.mu.Unlock()
	return p.claude(ctx, c)
}

// PutFile puts a file in every sandbox, for the agent to have written.
func (p *Provider) PutFile(path string, data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Files[path] = data
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
		"session_id": sid, "total_cost_usd": 0.25, "num_turns": 1,
		"usage":      map[string]any{"input_tokens": 10, "output_tokens": 200, "cache_read_input_tokens": 3000, "cache_creation_input_tokens": 400},
		"modelUsage": map[string]any{"claude-opus-5-5": map[string]any{"costUSD": 0.25}}})
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
	mu       sync.Mutex
	PRs      []session.PullRequest
	Status   *session.PRStatus // what PRStatus answers
	Comments []string          // "repo#number: body"
	Labeled  []string          // "repo#number: label"
	Err      error
	Identity store.Author // whom the host acts for
}

func (s *SCM) CloneURL(repo string) string                           { return "file:///remote/" + repo + ".git" }
func (s *SCM) Token(context.Context, string) (string, error)         { return "test-token", nil }
func (s *SCM) DefaultBranch(context.Context, string) (string, error) { return "main", nil }
func (s *SCM) Author(context.Context) store.Author                   { return s.Identity }

func (s *SCM) OpenPR(_ context.Context, pr session.PullRequest) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return "", s.Err
	}
	s.PRs = append(s.PRs, pr)
	return "https://github.com/" + pr.Repo + "/pull/" + fmt.Sprint(len(s.PRs)), nil
}

func (s *SCM) PRStatus(context.Context, string, string) (*session.PRStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Status, s.Err
}

func (s *SCM) Comment(_ context.Context, repo string, number int, body string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Comments = append(s.Comments, fmt.Sprintf("%s#%d: %s", repo, number, body))
	return fmt.Sprintf("https://github.com/%s/issues/%d#issuecomment-%d", repo, number, len(s.Comments)), s.Err
}

func (s *SCM) AddLabels(_ context.Context, repo string, number int, labels []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range labels {
		s.Labeled = append(s.Labeled, fmt.Sprintf("%s#%d: %s", repo, number, l))
	}
	return s.Err
}
