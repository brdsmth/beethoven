package agent

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// claude-tool-turn.ndjson is a real Claude Code transcript: one Bash tool
// call followed by a short reply, with partial-message streaming on.
func TestDecodeRealTranscript(t *testing.T) {
	f, err := os.Open("testdata/claude-tool-turn.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var events []Event
	res := Claude{}.Decode(f, func(e Event) { events = append(events, e) })

	if !res.OK || res.Text != "done" || res.Session != "00000000-0000-4000-8000-000000000001" || res.Turns != 2 || res.CostUSD <= 0 {
		t.Errorf("result = %+v", res)
	}
	var types []string
	for _, e := range events {
		types = append(types, e.Type)
	}
	if want := []string{ToolCall, ToolResult, Delta, Text}; !slices.Equal(types, want) {
		t.Fatalf("events = %v, want %v", types, want)
	}
	call := events[0].Data.(ToolData)
	if call.Name != "Bash" || call.Input.(map[string]any)["command"] != "echo hello-from-tool" {
		t.Errorf("tool call = %+v", call)
	}
	if r := events[1].Data.(ToolResultData); r.ID != call.ID || r.Output != "hello-from-tool" || r.IsError {
		t.Errorf("tool result = %+v", r)
	}
	if d := events[2].Data.(TextData); d.Text != "done" {
		t.Errorf("delta = %+v", d)
	}
}

func TestDecodeErrorsAndEdgeCases(t *testing.T) {
	lines := []string{
		`not json`,
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":"toolu_parent","message":{"content":[{"type":"text","text":"from a subagent"}]}}`,
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"content":[{"type":"tool_use","id":"t1","name":"mcp__beethoven__create_pull_request","input":{}}]}}`,
		`{"type":"user","session_id":"s1","parent_tool_use_id":null,"message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":[{"type":"text","text":"push rejected"},{"type":"image"}]}]}}`,
		`{"type":"result","subtype":"error_max_turns","is_error":true,"session_id":"s1","num_turns":50}`,
	}
	var events []Event
	res := Claude{}.Decode(strings.NewReader(strings.Join(lines, "\n")), func(e Event) { events = append(events, e) })

	if res.OK || res.Error != "agent stopped: error_max_turns" || res.Session != "s1" {
		t.Errorf("result = %+v", res)
	}
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	if d := events[0].Data.(TextData); d.Parent != "toolu_parent" {
		t.Errorf("subagent text lost its parent: %+v", d)
	}
	if c := events[1].Data.(ToolData); c.Name != "create_pull_request" {
		t.Errorf("tool name = %q, want the MCP prefix stripped", c.Name)
	}
	if r := events[2].Data.(ToolResultData); !r.IsError || r.Output != "push rejected\n[image]" {
		t.Errorf("tool result = %+v", r)
	}
}

func TestDecodeWithoutResult(t *testing.T) {
	res := Claude{}.Decode(strings.NewReader(`{"type":"system","subtype":"init","session_id":"s1"}`), func(Event) {})
	if res.OK || res.Error != "agent exited without a result" || res.Session != "s1" {
		t.Errorf("result = %+v", res)
	}
	if !(Claude{}).ResumeFailed(Result{Error: res.Error}, "No conversation found with session ID: x") {
		t.Error("ResumeFailed did not recognize a lost conversation")
	}
}

func TestCommand(t *testing.T) {
	inv := Claude{}.Command(Turn{Prompt: "fix it", Model: "opus", Resume: "c1", System: "be brief",
		Tools: &Tools{URL: "http://host/mcp", Token: "tok"}})
	args := strings.Join(inv.Args, " ")
	for _, want := range []string{"claude -p --output-format stream-json", "--model opus", "--resume c1", "--append-system-prompt be brief", "--dangerously-skip-permissions"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q: %s", want, args)
		}
	}
	if inv.Stdin != "fix it" {
		t.Errorf("prompt should go on stdin, got %q", inv.Stdin)
	}
	i := slices.Index(inv.Args, "--mcp-config")
	var cfg struct {
		MCPServers map[string]struct {
			Type, URL string
			Headers   map[string]string
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(inv.Args[i+1]), &cfg); err != nil {
		t.Fatal(err)
	}
	if s := cfg.MCPServers["beethoven"]; s.Type != "http" || s.URL != "http://host/mcp" || s.Headers["Authorization"] != "Bearer tok" {
		t.Errorf("mcp config = %+v", cfg)
	}
}
