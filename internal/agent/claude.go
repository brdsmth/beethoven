package agent

import (
	"bufio"
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

// Claude runs Claude Code headless: `claude -p --output-format stream-json`.
type Claude struct {
	Binary string // default "claude"
}

func (c Claude) Command(t Turn) Invocation {
	bin := c.Binary
	if bin == "" {
		bin = "claude"
	}
	args := []string{bin, "-p", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--dangerously-skip-permissions"}
	if t.Model != "" {
		args = append(args, "--model", t.Model)
	}
	if t.Effort != "" {
		args = append(args, "--effort", t.Effort)
	}
	if t.Resume != "" {
		args = append(args, "--resume", t.Resume)
	}
	if t.System != "" {
		args = append(args, "--append-system-prompt", t.System)
	}
	servers := map[string]any{}
	for _, s := range t.MCP {
		switch s.Type {
		case "http":
			servers[s.Name] = map[string]any{"type": "http", "url": s.URL, "headers": s.Headers}
		case "stdio":
			if len(s.Command) > 0 {
				servers[s.Name] = map[string]any{"type": "stdio", "command": s.Command[0], "args": s.Command[1:], "env": s.Env}
			}
		}
	}
	if t.Tools != nil {
		servers["beethoven"] = map[string]any{
			"type":    "http",
			"url":     t.Tools.URL,
			"headers": map[string]string{"Authorization": "Bearer " + t.Tools.Token},
		}
	}
	if len(servers) > 0 {
		cfg, _ := json.Marshal(map[string]any{"mcpServers": servers})
		args = append(args, "--mcp-config", string(cfg))
	}
	return Invocation{Args: args, Stdin: t.Prompt, Env: map[string]string{"DISABLE_AUTOUPDATER": "1"}}
}

// line is the union of the stream-json message shapes we read.
type line struct {
	Type      string  `json:"type"`
	Subtype   string  `json:"subtype"`
	SessionID string  `json:"session_id"`
	Parent    *string `json:"parent_tool_use_id"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
	// result
	IsError bool     `json:"is_error"`
	Result  string   `json:"result"`
	Cost    float64  `json:"total_cost_usd"`
	Turns   int      `json:"num_turns"`
	Errors  []string `json:"errors"`
	Usage   struct {
		Input      int64 `json:"input_tokens"`
		Output     int64 `json:"output_tokens"`
		CacheRead  int64 `json:"cache_read_input_tokens"`
		CacheWrite int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	ModelUsage map[string]struct {
		Cost float64 `json:"costUSD"`
	} `json:"modelUsage"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

const maxToolOutput = 16 << 10

func (Claude) Decode(r io.Reader, emit func(Event)) Result {
	var res Result
	sawResult := false
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		parent := ""
		if l.Parent != nil {
			parent = *l.Parent
		}
		if l.SessionID != "" {
			res.Session = l.SessionID
		}
		switch l.Type {
		case "stream_event":
			if parent == "" && l.Event.Type == "content_block_delta" && l.Event.Delta.Type == "text_delta" {
				emit(Event{Delta, TextData{Text: l.Event.Delta.Text}})
			}
		case "assistant":
			for _, b := range blocks(l.Message.Content) {
				switch b.Type {
				case "text":
					if strings.TrimSpace(b.Text) != "" {
						emit(Event{Text, TextData{Text: b.Text, Parent: parent}})
					}
				case "thinking":
					if b.Thinking != "" {
						emit(Event{Thinking, TextData{Text: b.Thinking, Parent: parent}})
					}
				case "tool_use":
					var input any
					json.Unmarshal(b.Input, &input)
					emit(Event{ToolCall, ToolData{ID: b.ID, Name: toolName(b.Name), Input: input, Parent: parent}})
				}
			}
		case "user":
			for _, b := range blocks(l.Message.Content) {
				if b.Type == "tool_result" {
					emit(Event{ToolResult, ToolResultData{ID: b.ToolUseID, Output: truncate(flatten(b.Content), maxToolOutput), IsError: b.IsError}})
				}
			}
		case "result":
			sawResult = true
			res.OK = !l.IsError && l.Subtype == "success"
			res.Text, res.CostUSD, res.Turns = l.Result, l.Cost, l.Turns
			res.Usage = Usage(l.Usage)
			best := -1.0
			for model, u := range l.ModelUsage {
				if u.Cost > best || u.Cost == best && model < res.Model {
					res.Model, best = model, u.Cost
				}
			}
			if !res.OK {
				res.Error = l.Result
				if res.Error == "" {
					res.Error = strings.Join(l.Errors, "; ")
				}
				if res.Error == "" {
					res.Error = "agent stopped: " + l.Subtype
				}
			}
		}
	}
	io.Copy(io.Discard, r) // drain after a scanner error so the writer never blocks
	if !sawResult {
		res.OK, res.Error = false, "agent exited without a result"
	}
	return res
}

func (Claude) ResumeFailed(res Result, stderr string) bool {
	return !res.OK && strings.Contains(res.Error+stderr, "No conversation found")
}

func blocks(raw json.RawMessage) []block {
	var bs []block
	if json.Unmarshal(raw, &bs) == nil {
		return bs
	}
	return nil
}

// flatten turns tool_result content (a string or a list of blocks) into text.
func flatten(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []string
	for _, b := range blocks(raw) {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image":
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "\n")
}

// toolName strips the MCP prefix from our own tools: mcp__beethoven__x -> x.
func toolName(n string) string {
	return strings.TrimPrefix(n, "mcp__beethoven__")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n… [truncated " + strconv.Itoa(len(s)-n) + " bytes]"
}
