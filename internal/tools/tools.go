// Package tools serves the agent's tools over MCP. Every coding agent that
// speaks MCP gets the same tools, and each call acts as the session that the
// caller's bearer token names.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brdsmth/beethoven/internal/capability"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

func Handler(m *session.Manager) http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{Name: "beethoven", Version: "1"}, nil)
	t := &tools{m: m}
	mcp.AddTool(srv, &mcp.Tool{Name: "create_pull_request", Description: "Push this session's branch to GitHub and open a pull request, " +
		"or return the one already open. Commit your changes first: uncommitted changes are not included."}, t.createPR)
	mcp.AddTool(srv, &mcp.Tool{Name: "pull_request_status", Description: "Ask GitHub about this session's pull request: its state, " +
		"whether it can merge (GitHub's own mergeable status, which accounts for conflicts, branch protection and required checks) " +
		"and its CI checks. You have no GitHub credentials or gh CLI in the sandbox; use this instead."}, t.prStatus)
	mcp.AddTool(srv, &mcp.Tool{Name: "comment", Description: "Post a comment on an issue or pull request in one of this session's " +
		"repositories, as the person the session works for. Use it to answer the issue or pull request an automation started you on."}, t.comment)
	mcp.AddTool(srv, &mcp.Tool{Name: "add_labels", Description: "Add labels to an issue or pull request in one of this session's repositories. " +
		"Labels the repository doesn't have yet are created."}, t.addLabels)
	mcp.AddTool(srv, &mcp.Tool{Name: "show_file", Description: "Open a file from this sandbox in the viewer beside the conversation: " +
		"images, HTML mockups, Markdown, PDFs, code, CSV and more. Call it when the person asks to see a file, and after you make " +
		"something visual (a mockup, a screenshot, a chart, a generated image). Showing the same file again after changing it " +
		"adds a new version. HTML is shown on its own, so inline its CSS, scripts and images. Up to 25 MB."}, t.showFile)
	mcp.AddTool(srv, &mcp.Tool{Name: "spawn_session", Description: "Start a child session: another agent in its own sandbox, on its own branch, " +
		"working in parallel. Returns the child's id immediately. The child cannot see your uncommitted work, so give it self-contained instructions."}, t.spawn)
	mcp.AddTool(srv, &mcp.Tool{Name: "send_prompt", Description: "Queue a follow-up prompt on one of your child sessions."}, t.send)
	mcp.AddTool(srv, &mcp.Tool{Name: "get_session", Description: "Get a child session's status, latest results, branch and pull request. " +
		"Set wait_seconds to block until the child stops running (or the wait elapses)."}, t.get)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_sessions", Description: "List your child sessions with their status."}, t.list)
	mcp.AddTool(srv, &mcp.Tool{Name: "stop_session", Description: "Stop a child session's current work and cancel its queued prompts."}, t.stop)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_capabilities", Description: "List the toolchains this sandbox can be given (mobile apps with Expo, " +
		"Android builds, Go, Rust, Bun, Python with uv): which are set up, which the repositories look like they need, and the secrets each uses."}, t.listCapabilities)
	mcp.AddTool(srv, &mcp.Tool{Name: "add_capability", Description: "Install a toolchain in this sandbox now, with a skill that explains how to use it, " +
		"and keep it for later sessions: in the session's project (environment), or in the repository's .beethoven/capabilities file, which you then commit."}, t.addCapability)

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

type prStatusIn struct {
	Repo string `json:"repo,omitempty" jsonschema:"owner/name of the repository; required when the session has several"`
}

func (t *tools) prStatus(ctx context.Context, req *mcp.CallToolRequest, in prStatusIn) (*mcp.CallToolResult, any, error) {
	me, err := t.caller(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	st, err := t.m.PRStatus(ctx, me.ID, in.Repo)
	if err != nil {
		return nil, nil, err
	}
	if st == nil {
		return text("No pull request from %s yet. Open one with create_pull_request.", me.Branch), nil, nil
	}
	return jsonText(st), nil, nil
}

type commentIn struct {
	Repo   string `json:"repo,omitempty" jsonschema:"owner/name of the repository; required when the session has several"`
	Number int    `json:"number" jsonschema:"the issue or pull request number"`
	Body   string `json:"body" jsonschema:"the comment, in Markdown"`
}

func (t *tools) comment(ctx context.Context, req *mcp.CallToolRequest, in commentIn) (*mcp.CallToolResult, any, error) {
	me, err := t.caller(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	url, err := t.m.Comment(ctx, me.ID, in.Repo, in.Number, in.Body)
	if err != nil {
		return nil, nil, err
	}
	return text("Commented: %s", url), nil, nil
}

type labelsIn struct {
	Repo   string   `json:"repo,omitempty" jsonschema:"owner/name of the repository; required when the session has several"`
	Number int      `json:"number" jsonschema:"the issue or pull request number"`
	Labels []string `json:"labels" jsonschema:"labels to add"`
}

func (t *tools) addLabels(ctx context.Context, req *mcp.CallToolRequest, in labelsIn) (*mcp.CallToolResult, any, error) {
	me, err := t.caller(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	if err := t.m.AddLabels(ctx, me.ID, in.Repo, in.Number, in.Labels); err != nil {
		return nil, nil, err
	}
	return text("Added %s to #%d.", strings.Join(in.Labels, ", "), in.Number), nil, nil
}

type showFileIn struct {
	Path  string `json:"path" jsonschema:"the file: absolute, or relative to your working directory"`
	Title string `json:"title,omitempty" jsonschema:"a short caption, e.g. 'Checkout page, mobile'"`
}

func (t *tools) showFile(ctx context.Context, req *mcp.CallToolRequest, in showFileIn) (*mcp.CallToolResult, any, error) {
	me, err := t.caller(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	a, err := t.m.ShowFile(ctx, me.ID, in.Path, in.Title)
	if err != nil {
		return nil, nil, err
	}
	return text("Showing %s (%s, %d bytes) in the viewer.", a.Path, a.ContentType, a.Size), nil, nil
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

func (t *tools) listCapabilities(ctx context.Context, req *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
	me, err := t.caller(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	have, err := t.m.Capabilities(ctx, me.ID)
	if err != nil {
		return nil, nil, err
	}
	type secretOut struct {
		Name     string `json:"name"`
		Required bool   `json:"required"`
		Why      string `json:"why"`
		Set      bool   `json:"set"`
	}
	type capOut struct {
		ID          string      `json:"id"`
		Name        string      `json:"name"`
		Description string      `json:"description"`
		Enabled     bool        `json:"enabled"`
		Suggested   bool        `json:"suggested"`
		Secrets     []secretOut `json:"secrets,omitempty"`
	}
	var out []capOut
	for _, c := range capability.Catalog {
		o := capOut{ID: c.ID, Name: c.Name, Description: c.Description,
			Enabled: slices.Contains(have.Enabled, c.ID), Suggested: slices.Contains(have.Suggested, c.ID)}
		for _, s := range c.Secrets {
			o.Secrets = append(o.Secrets, secretOut{s.Name, s.Required, s.Why, slices.Contains(have.SecretsSet, s.Name)})
		}
		out = append(out, o)
	}
	return jsonText(out), nil, nil
}

type addCapabilityIn struct {
	ID string `json:"id" jsonschema:"the capability's id, from list_capabilities"`
}

func (t *tools) addCapability(ctx context.Context, req *mcp.CallToolRequest, in addCapabilityIn) (*mcp.CallToolResult, any, error) {
	me, err := t.caller(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	added, err := t.m.AddCapability(ctx, me.ID, strings.TrimSpace(in.ID), "agent")
	if err != nil {
		return nil, nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Installed %s. Read ~/.claude/skills/%s/SKILL.md before you continue.\n", added.Name, added.ID)
	switch added.Kept {
	case "environment":
		fmt.Fprintf(&b, "It is kept in the %q project, so its later sessions have it too.\n", added.Where)
	case "repository":
		b.WriteString("It is recorded in .beethoven/capabilities in the primary repository: commit that file with your work so later sessions have it too.\n")
	default:
		b.WriteString("It is kept for this session only: there is no project or repository to record it in.\n")
	}
	for _, w := range added.Warnings {
		fmt.Fprintf(&b, "Warning: %s\n", w)
	}
	return text("%s", b.String()), nil, nil
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
