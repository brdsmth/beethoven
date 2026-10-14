// Package tools serves the agent's tools over MCP. Every coding agent that
// speaks MCP gets the same tools, and each call acts as the session that the
// caller's bearer token names.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

func Handler(m *session.Manager) http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{Name: "beethoven", Version: "1"}, nil)
	t := &tools{m: m}
	mcp.AddTool(srv, &mcp.Tool{Name: "create_pull_request", Description: "Push this session's branch to GitHub and open a pull request, " +
		"or return the one already open. Commit your changes first: uncommitted changes are not included."}, t.createPR)
	mcp.AddTool(srv, &mcp.Tool{Name: "spawn_session", Description: "Start a child session: another agent in its own sandbox, on its own branch, " +
		"working in parallel. Returns the child's id immediately. The child cannot see your uncommitted work, so give it self-contained instructions."}, t.spawn)
	mcp.AddTool(srv, &mcp.Tool{Name: "send_prompt", Description: "Queue a follow-up prompt on one of your child sessions."}, t.send)
	mcp.AddTool(srv, &mcp.Tool{Name: "get_session", Description: "Get a child session's status, latest results, branch and pull request. " +
		"Set wait_seconds to block until the child stops running (or the wait elapses)."}, t.get)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_sessions", Description: "List your child sessions with their status."}, t.list)
	mcp.AddTool(srv, &mcp.Tool{Name: "stop_session", Description: "Stop a child session's current work and cancel its queued prompts."}, t.stop)

	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// Sandboxes reach the server as host.docker.internal; every call is
		// authenticated by a session token, which DNS rebinding cannot forge.
		DisableLocalhostProtection: true,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := m.Authenticate(r.Context(), bearer(r.Header)); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

type tools struct{ m *session.Manager }

type none struct{}

func (t *tools) caller(ctx context.Context, req *mcp.CallToolRequest) (store.Session, error) {
	if req.Extra == nil {
		return store.Session{}, session.ErrUnauthorized
	}
	return t.m.Authenticate(ctx, bearer(req.Extra.Header))
}

// child resolves id to a direct child of the caller.
func (t *tools) child(ctx context.Context, req *mcp.CallToolRequest, id string) (store.Session, store.Session, error) {
	me, err := t.caller(ctx, req)
	if err != nil {
		return me, store.Session{}, err
	}
	c, err := t.m.Session(ctx, id)
	if err != nil || c.ParentID != me.ID {
		return me, c, fmt.Errorf("no child session %q", id)
	}
	return me, c, nil
}

type prIn struct {
	Repo  string `json:"repo,omitempty" jsonschema:"owner/name of the repository; required when the session has several"`
	Title string `json:"title" jsonschema:"pull request title"`
	Body  string `json:"body" jsonschema:"pull request description in Markdown"`
	Draft *bool  `json:"draft,omitempty" jsonschema:"open as a draft; defaults to the server's setting"`
}

func (t *tools) createPR(ctx context.Context, req *mcp.CallToolRequest, in prIn) (*mcp.CallToolResult, any, error) {
	me, err := t.caller(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	url, err := t.m.OpenPR(ctx, me.ID, in.Repo, in.Title, in.Body, in.Draft)
	if session.IsUncommittedWarning(err) {
		return text("Pull request: %s\nWarning: %v.", url, err), nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return text("Pull request: %s", url), nil, nil
}

type spawnIn struct {
	Prompt     string `json:"prompt" jsonschema:"complete, self-contained instructions for the child"`
	Title      string `json:"title,omitempty" jsonschema:"short title for the child session"`
	Repo       string `json:"repo,omitempty" jsonschema:"owner/name to work on alone; defaults to this session's workspace"`
	BaseBranch string `json:"base_branch,omitempty" jsonschema:"branch to start from, with repo"`
	Model      string `json:"model,omitempty" jsonschema:"model override"`
}

func (t *tools) spawn(ctx context.Context, req *mcp.CallToolRequest, in spawnIn) (*mcp.CallToolResult, any, error) {
	me, err := t.caller(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	spawn := session.Request{Prompt: in.Prompt, Title: in.Title, Model: in.Model, Author: me.Author, Source: "agent", ParentID: me.ID}
	if in.Repo != "" {
		spawn.Repos = []store.RepoRef{{Repo: in.Repo, BaseBranch: in.BaseBranch}}
	}
	child, _, err := t.m.Submit(ctx, spawn)
	if err != nil {
		return nil, nil, err
	}
	return text("Started child session %s on branch %s.", child.ID, child.Branch), nil, nil
}

type sendIn struct {
	SessionID string `json:"session_id" jsonschema:"child session id"`
	Prompt    string `json:"prompt" jsonschema:"follow-up instructions"`
}

func (t *tools) send(ctx context.Context, req *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, any, error) {
	me, c, err := t.child(ctx, req, in.SessionID)
	if err != nil {
		return nil, nil, err
	}
	p, err := t.m.Enqueue(ctx, c.ID, session.PromptInput{Content: in.Prompt, Author: me.Author, Source: "agent"})
	if err != nil {
		return nil, nil, err
	}
	return text("Queued prompt %s on %s.", p.ID, c.ID), nil, nil
}

type getIn struct {
	SessionID   string `json:"session_id" jsonschema:"child session id"`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"wait up to this many seconds (max 600) for the child to stop running"`
}

func (t *tools) get(ctx context.Context, req *mcp.CallToolRequest, in getIn) (*mcp.CallToolResult, any, error) {
	_, c, err := t.child(ctx, req, in.SessionID)
	if err != nil {
		return nil, nil, err
	}
	if in.WaitSeconds > 0 && c.Status == "running" {
		c = t.waitIdle(ctx, c, time.Duration(min(in.WaitSeconds, 600))*time.Second)
	}
	prompts, err := t.m.Store().Prompts(ctx, c.ID)
	if err != nil {
		return nil, nil, err
	}
	type promptOut struct {
		Status string `json:"status"`
		Prompt string `json:"prompt"`
		Result string `json:"result,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	out := struct {
		ID      string          `json:"id"`
		Title   string          `json:"title"`
		Status  string          `json:"status"`
		Branch  string          `json:"branch"`
		Repos   []store.RepoRef `json:"repositories"`
		Prompts []promptOut     `json:"recent_prompts"`
	}{ID: c.ID, Title: c.Title, Status: c.Status, Branch: c.Branch, Repos: c.Repos}
	for _, p := range prompts[max(0, len(prompts)-3):] {
		out.Prompts = append(out.Prompts, promptOut{p.Status, clip(p.Content, 500), clip(p.Result, 6000), p.Error})
	}
	return jsonText(out), nil, nil
}

// waitIdle blocks until the session stops running, the timeout passes, or ctx ends.
func (t *tools) waitIdle(ctx context.Context, s store.Session, d time.Duration) store.Session {
	sub := t.m.Hub().Subscribe(s.ID)
	defer t.m.Hub().Unsubscribe(sub)
	if cur, err := t.m.Session(ctx, s.ID); err == nil && cur.Status != "running" {
		return cur // finished before we subscribed
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case e, ok := <-sub.C:
			if !ok {
				cur, _ := t.m.Session(ctx, s.ID)
				return cur
			}
			if e.Type == session.EvSession {
				var cur store.Session
				if json.Unmarshal(e.Data, &cur) == nil && cur.Status != "running" {
					return cur
				}
			}
		case <-timer.C:
			cur, _ := t.m.Session(ctx, s.ID)
			return cur
		case <-ctx.Done():
			return s
		}
	}
}

func (t *tools) list(ctx context.Context, req *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
	me, err := t.caller(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	children, err := t.m.Store().Sessions(ctx, store.SessionFilter{ParentID: me.ID})
	if err != nil {
		return nil, nil, err
	}
	if len(children) == 0 {
		return text("No child sessions."), nil, nil
	}
	var b strings.Builder
	for _, c := range children {
		fmt.Fprintf(&b, "%s  %-8s  %s  %s\n", c.ID, c.Status, c.Branch, c.Title)
	}
	return text("%s", b.String()), nil, nil
}

type stopIn struct {
	SessionID string `json:"session_id" jsonschema:"child session id"`
}

func (t *tools) stop(ctx context.Context, req *mcp.CallToolRequest, in stopIn) (*mcp.CallToolResult, any, error) {
	_, c, err := t.child(ctx, req, in.SessionID)
	if err != nil {
		return nil, nil, err
	}
	if err := t.m.Stop(ctx, c.ID); err != nil {
		return nil, nil, err
	}
	return text("Stopped %s.", c.ID), nil, nil
}

func bearer(h http.Header) string {
	return strings.TrimPrefix(h.Get("Authorization"), "Bearer ")
}

func text(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}}}
}

func jsonText(v any) *mcp.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return text("error: %v", err)
	}
	return text("%s", b)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}
