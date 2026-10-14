// Package agent adapts coding-agent CLIs to one shape: build the command for
// a turn, then decode its output stream into normalized events.
package agent

import "io"

// Turn is one prompt for the agent to work on.
type Turn struct {
	Prompt string
	Model  string
	Effort string      // low, medium, high, xhigh or max; "" = the agent's default
	Resume string      // conversation id from a previous turn's Result
	System string      // appended to the agent's system prompt
	Tools  *Tools      // the server's own MCP tools
	MCP    []MCPServer // further MCP servers to connect to
}

type Tools struct {
	URL   string
	Token string
}

// MCPServer is an additional tool server: remote over HTTP, or a local
// command speaking stdio inside the sandbox.
type MCPServer struct {
	Name    string
	Type    string // http | stdio
	URL     string
	Headers map[string]string
	Command []string
	Env     map[string]string
}

// Invocation is the process to run for a turn.
type Invocation struct {
	Args  []string
	Env   map[string]string
	Stdin string
}

// Event types emitted while a turn runs.
const (
	Text       = "text"        // Data: TextData — a finished assistant message
	Delta      = "delta"       // Data: TextData — partial text, for live display only
	Thinking   = "thinking"    // Data: TextData
	ToolCall   = "tool"        // Data: ToolData
	ToolResult = "tool.result" // Data: ToolResultData
)

type Event struct {
	Type string
	Data any
}

type TextData struct {
	Text   string `json:"text"`
	Parent string `json:"parent,omitempty"` // tool call id when emitted by a subagent
}

type ToolData struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Input  any    `json:"input"`
	Parent string `json:"parent,omitempty"`
}

type ToolResultData struct {
	ID      string `json:"id"`
	Output  string `json:"output"`
	IsError bool   `json:"is_error,omitempty"`
}

// Result summarizes a finished turn.
type Result struct {
	OK      bool
	Error   string
	Text    string // final response
	Session string // conversation id, to resume the next turn
	CostUSD float64
	Turns   int
}

type Harness interface {
	Command(t Turn) Invocation
	// Decode consumes the process's stdout until EOF, emitting events as they
	// arrive, and returns the turn's result. It must drain r.
	Decode(r io.Reader, emit func(Event)) Result
	// ResumeFailed reports whether a failed turn failed only because its
	// conversation could not be resumed, so a fresh one is worth trying.
	ResumeFailed(res Result, stderr string) bool
}
