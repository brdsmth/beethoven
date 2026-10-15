package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/brdsmth/beethoven/internal/agent"
	"github.com/brdsmth/beethoven/internal/sandbox"
	"github.com/brdsmth/beethoven/internal/store"
)

// runner owns one session: its queue, its sandbox and its event log.
//
// Lock order is sbMu → mu → emitMu; nothing holding mu takes sbMu.
type runner struct {
	m    *Manager
	id   string
	gone atomic.Bool // deleted

	mu   sync.Mutex // guards s, turn, last
	s    store.Session
	turn *turn
	last string // status of the most recently finished prompt

	sbMu    sync.Mutex // serializes sandbox lifecycle; guards leases, idleGen
	leases  int        // work currently using the sandbox
	idleGen int        // invalidates pending idle stops

	emitMu sync.Mutex // keeps sequence order equal to publish order
}

type turn struct {
	prompt  store.Prompt
	cancel  context.CancelFunc
	timeout time.Duration
	stopped bool
}

type outcome struct {
	status, err, result string
	cost                float64
}

// ------------------------------------------------------------------ queue

func (r *runner) enqueue(ctx context.Context, in PromptInput) (store.Prompt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gone.Load() {
		return store.Prompt{}, ErrNotFound
	}
	p := store.Prompt{ID: store.NewID(), SessionID: r.id, Content: in.Content, Model: in.Model, Effort: in.Effort,
		Author: in.Author, Source: in.Source, DedupKey: in.DedupKey, AutomationID: in.AutomationID}
	if p.Author == (store.Author{}) {
		p.Author = r.s.Author
	}
	created, err := r.m.db.CreatePrompt(ctx, &p)
	if err != nil || !created {
		return p, err
	}
	r.emit(p.ID, EvPrompt, p)
	r.s.Archived = false
	r.s.Status = "running"
	r.save()
	r.next()
	return p, nil
}

func (r *runner) kick() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next()
}

// next starts the oldest queued prompt unless one is already running. mu held.
func (r *runner) next() {
	if r.turn != nil || r.gone.Load() || r.m.ctx.Err() != nil {
		return
	}
	p, err := r.m.db.ClaimNext(context.Background(), r.id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			r.m.log.Error("claim prompt", "session", r.id, "err", err)
		}
		r.settle()
		return
	}
	timeout := r.m.cfg.TurnTimeout
	if eff, err := r.m.Effective(context.Background(), r.s.EnvironmentID); err == nil {
		timeout = eff.TurnTimeout
	}
	ctx, cancel := context.WithTimeout(r.m.ctx, timeout)
	r.turn = &turn{prompt: p, cancel: cancel, timeout: timeout}
	if r.s.Status != "running" {
		r.s.Status = "running"
		r.save()
	}
	r.emit(p.ID, EvStart, map[string]string{"id": p.ID})
	r.m.turns.Add(1)
	go func() {
		defer r.m.turns.Done()
		defer cancel()
		o := r.execute(ctx, p)
		r.finish(p, o, ctx.Err())
	}()
}

func (r *runner) finish(p store.Prompt, o outcome, ctxErr error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.turn
	r.turn = nil
	p.Status, p.Error, p.Result, p.CostUSD = o.status, o.err, o.result, o.cost
	switch {
	case t.stopped:
		p.Status, p.Error = store.Cancelled, "stopped"
	case errors.Is(ctxErr, context.DeadlineExceeded):
		p.Status, p.Error = store.Failed, fmt.Sprintf("timed out after %s", t.timeout)
	case ctxErr != nil:
		p.Status, p.Error = store.Failed, "interrupted: the server is shutting down"
	}
	if r.gone.Load() {
		return
	}
	if err := r.m.db.FinishPrompt(context.Background(), &p); err != nil {
		r.m.log.Error("finish prompt", "session", r.id, "err", err)
	}
	r.last = p.Status
	r.s.CostUSD += p.CostUSD
	r.save()
	r.emit(p.ID, EvEnd, endData(p))
	s := r.s
	go r.m.finished(s, p)
	r.next()
}

// settle derives the session status once its queue is empty. mu held.
func (r *runner) settle() {
	status := map[string]string{store.Done: "done", store.Failed: "failed", store.Cancelled: "stopped"}[r.last]
	if status == "" {
		status = "idle"
	}
	if r.s.Status != status {
		r.s.Status = status
		r.save()
	}
}

// stop cancels promptID (or, if empty, every queued prompt) and stops the
// running prompt when it is the target.
func (r *runner) stop(promptID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cancelled, err := r.m.db.CancelQueued(context.Background(), r.id, promptID)
	if err != nil {
		r.m.log.Error("cancel prompts", "session", r.id, "err", err)
	}
	for _, p := range cancelled {
		r.emit(p.ID, EvEnd, endData(p))
	}
	if r.turn != nil && (promptID == "" || r.turn.prompt.ID == promptID) {
		r.turn.stopped = true
		r.turn.cancel()
	}
	if r.turn == nil && len(cancelled) > 0 {
		r.last = store.Cancelled
		r.settle()
	}
}

// ------------------------------------------------------------------- turns

func (r *runner) execute(ctx context.Context, p store.Prompt) outcome {
	id, release, err := r.acquire(ctx)
	if err != nil {
		return outcome{status: store.Failed, err: "sandbox: " + err.Error()}
	}
	defer release()
	s := r.snapshot()
	eff, err := r.m.Effective(ctx, s.EnvironmentID)
	if err != nil {
		return outcome{status: store.Failed, err: err.Error()}
	}
	env, err := r.m.turnEnv(ctx, s, p, eff)
	if err != nil {
		return outcome{status: store.Failed, err: err.Error()}
	}
	servers, err := r.m.mcpServers(ctx, s)
	if err != nil {
		return outcome{status: store.Failed, err: err.Error()}
	}
	resume := s.AgentSession
	for attempt := 0; ; attempt++ {
		t := agent.Turn{
			Prompt: p.Content,
			Model:  firstOf(p.Model, s.Model, eff.Model),
			Effort: firstOf(p.Effort, s.Effort, eff.Effort),
			Resume: resume,
			System: systemPrompt(s, eff),
			Tools:  &agent.Tools{URL: r.m.cfg.ToolsURL, Token: r.m.Token(s.ID)},
			MCP:    servers,
		}
		res, stderr, code, err := r.runAgent(ctx, id, p.ID, t, maps.Clone(env))
		if res.Session != "" {
			r.update(func(s *store.Session) { s.AgentSession = res.Session })
		}
		switch {
		case ctx.Err() != nil:
			return outcome{status: store.Failed, err: ctx.Err().Error(), cost: res.CostUSD}
		case err != nil:
			return outcome{status: store.Failed, err: "exec: " + err.Error()}
		case !res.OK && resume != "" && attempt == 0 && r.m.agent.ResumeFailed(res, stderr):
			r.emit(p.ID, EvWarning, map[string]string{"message": "could not resume the previous conversation; starting a new one"})
			resume = ""
			continue
		case res.OK:
			return outcome{status: store.Done, result: res.Text, cost: res.CostUSD}
		}
		msg := res.Error
		if tail := strings.TrimSpace(stderr); tail != "" {
			msg += fmt.Sprintf(" (exit %d): %s", code, clip(tail, 2000))
		}
		return outcome{status: store.Failed, err: msg, result: res.Text, cost: res.CostUSD}
	}
}

func (r *runner) runAgent(ctx context.Context, box, promptID string, t agent.Turn, env map[string]string) (agent.Result, string, int, error) {
	inv := r.m.agent.Command(t)
	maps.Copy(env, inv.Env)
	pr, pw := io.Pipe()
	results := make(chan agent.Result, 1)
	go func() {
		results <- r.m.agent.Decode(pr, func(e agent.Event) {
			if e.Type == agent.Delta {
				r.publish(promptID, e.Type, e.Data)
			} else {
				r.emit(promptID, e.Type, e.Data)
			}
		})
	}()
	stderr := &tail{max: 8 << 10}
	code, err := r.m.box.Exec(ctx, box, sandbox.Cmd{Args: inv.Args, Env: env, Dir: Workdir(r.snapshot()),
		Stdin: strings.NewReader(inv.Stdin), Stdout: pw, Stderr: stderr})
	pw.Close()
	return <-results, stderr.String(), code, err
}

// ----------------------------------------------------------------- sandbox

// acquire brings the sandbox up and holds it until release is called;
// a held sandbox is never stopped for idleness.
func (r *runner) acquire(ctx context.Context) (string, func(), error) {
	r.sbMu.Lock()
	defer r.sbMu.Unlock()
	r.idleGen++
	id, err := r.ensure(ctx)
	if err != nil {
		r.armIdle()
		return "", nil, err
	}
	r.leases++
	var once sync.Once
	return id, func() {
		once.Do(func() {
			r.sbMu.Lock()
			defer r.sbMu.Unlock()
			r.leases--
			r.armIdle()
		})
	}, nil
}

// armIdle schedules an idle stop. sbMu held.
func (r *runner) armIdle() {
	if r.leases > 0 {
		return
	}
	r.idleGen++
	gen := r.idleGen
	idle := r.m.cfg.IdleTimeout
	if eff, err := r.m.Effective(context.Background(), r.snapshot().EnvironmentID); err == nil {
		idle = eff.IdleTimeout
	}
	time.AfterFunc(idle, func() {
		r.sbMu.Lock()
		defer r.sbMu.Unlock()
		if gen == r.idleGen && r.leases == 0 && !r.gone.Load() {
			r.stopLocked("idle")
		}
	})
}

// ensure starts the sandbox if needed and boots the workspace whenever the
// sandbox was not already running — including when it was stopped behind
// our back. sbMu held.
func (r *runner) ensure(ctx context.Context) (string, error) {
	s := r.snapshot()
	ready := s.Sandbox == "ready"
	if !ready {
		r.setSandbox("starting", "", "")
	}
	eff, err := r.m.Effective(ctx, s.EnvironmentID)
	if err != nil {
		return "", err
	}
	// Image and resources apply when the sandbox is created; an existing
	// sandbox keeps what it was created with.
	id, started, err := r.m.box.Start(ctx, sandbox.Spec{Name: "beethoven-" + s.ID, Image: eff.Image,
		CPUs: eff.CPUs, MemoryMB: eff.MemoryMB, Env: map[string]string{"BEETHOVEN_SESSION": s.ID}})
	if err != nil {
		r.setSandbox("failed", "", err.Error())
		return "", err
	}
	if ready && !started {
		return id, nil
	}
	if ready {
		r.setSandbox("starting", "", "the sandbox had stopped")
	}
	r.update(func(s *store.Session) { s.SandboxID = id })
	if err := r.boot(ctx, id, s, eff); err != nil {
		r.setSandbox("failed", "", err.Error())
		return "", err
	}
	r.setSandbox("ready", "", "")
	return id, nil
}

func (r *runner) boot(ctx context.Context, id string, s store.Session, eff Effective) error {
	env, err := r.m.baseEnv(ctx, s, eff)
	if err != nil {
		return err
	}
	env["WORK_BRANCH"], env["WORKDIR"] = s.Branch, Workdir(s)
	if len(s.Repos) > 0 {
		var lines, tokens []string
		for _, ref := range s.Repos {
			tok, err := r.m.scm.Token(ctx, ref.Repo)
			if err != nil {
				return err
			}
			lines = append(lines, ref.Dir()+" "+r.m.scm.CloneURL(ref.Repo)+" "+firstOf(ref.BaseBranch, "-"))
			tokens = append(tokens, ref.Dir()+" "+tok)
		}
		env["REPOS"], env["GH_TOKENS"] = strings.Join(lines, "\n"), strings.Join(tokens, "\n")
	}
	if e := eff.Environment; e != nil {
		env["ENV_SETUP"], env["ENV_SETUP_ID"], env["ENV_START"] = e.SetupScript, shortHash(e.SetupScript), e.StartScript
	}
	out := &bootOutput{r: r, phase: "start"}
	code, err := r.m.box.Exec(ctx, id, sandbox.Cmd{Args: []string{"sh", "-c", bootScript}, Env: env, Stdout: out, Stderr: out})
	out.Flush()
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("boot failed during %s (exit %d): %s", out.phase, code, clip(out.tail.String(), 2000))
	}
	return nil
}

// reclaim stops a sandbox left running by a previous server process,
// unless work has already started using it again.
func (r *runner) reclaim() {
	r.sbMu.Lock()
	defer r.sbMu.Unlock()
	if s := r.snapshot(); r.leases == 0 && s.Sandbox == "stopped" && s.SandboxID != "" {
		if err := r.m.box.Stop(context.Background(), s.SandboxID); err != nil {
			r.m.log.Warn("stop sandbox", "session", r.id, "err", err)
		}
	}
}

func (r *runner) stopSandbox(reason string) {
	r.sbMu.Lock()
	defer r.sbMu.Unlock()
	r.stopLocked(reason)
}

// stopLocked halts the sandbox, keeping its filesystem. sbMu held.
func (r *runner) stopLocked(reason string) {
	s := r.snapshot()
	if s.SandboxID == "" || s.Sandbox == "stopped" || s.Sandbox == "none" {
		return
	}
	r.idleGen++
	if err := r.m.box.Stop(context.Background(), s.SandboxID); err != nil {
		r.m.log.Warn("stop sandbox", "session", r.id, "err", err)
	}
	r.setSandbox("stopped", "", reason)
}

func (r *runner) destroy(ctx context.Context) error {
	r.mu.Lock()
	r.gone.Store(true)
	if r.turn != nil {
		r.turn.stopped = true
		r.turn.cancel()
	}
	r.mu.Unlock()
	r.sbMu.Lock()
	defer r.sbMu.Unlock()
	r.idleGen++
	if id := r.snapshot().SandboxID; id != "" {
		if err := r.m.box.Destroy(ctx, id); err != nil {
			r.gone.Store(false)
			return err
		}
	}
	return r.m.db.DeleteSession(ctx, r.id)
}

func (r *runner) setSandbox(state, phase, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.s.Sandbox != state {
		r.s.Sandbox = state
		r.save()
	}
	promptID := ""
	if r.turn != nil {
		promptID = r.turn.prompt.ID
	}
	r.emit(promptID, EvSandbox, map[string]string{"state": state, "phase": phase, "message": message})
}

// --------------------------------------------------------------------- PRs

const pushScript = `set -e
if [ -n "$(git status --porcelain)" ]; then echo "::dirty"; fi
git -c credential.helper= \
    -c 'credential.helper=!f() { test "$1" = get && echo username=x-access-token && echo "password=${GH_TOKEN}"; }; f' \
    push --force --quiet origin "HEAD:refs/heads/$BRANCH"`

func (r *runner) openPR(ctx context.Context, repo, title, body string, draft *bool) (string, error) {
	s := r.snapshot()
	i := slices.IndexFunc(s.Repos, func(ref store.RepoRef) bool { return strings.EqualFold(ref.Repo, repo) })
	switch {
	case len(s.Repos) == 0:
		return "", fmt.Errorf("%w: this session has no repository", ErrInvalid)
	case repo == "" && len(s.Repos) == 1:
		i = 0
	case repo == "":
		return "", fmt.Errorf("%w: say which repository: %s", ErrInvalid, strings.Join(store.RepoNames(s.Repos), ", "))
	case i < 0:
		return "", fmt.Errorf("%w: %s is not in this session", ErrInvalid, repo)
	}
	ref := s.Repos[i]
	eff, err := r.m.Effective(ctx, s.EnvironmentID)
	if err != nil {
		return "", err
	}
	if draft == nil {
		draft = &eff.DraftPRs
	}
	token, err := r.m.scm.Token(ctx, ref.Repo)
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", fmt.Errorf("no GitHub credential can push to %s: connect GitHub in Settings", ref.Repo)
	}
	id, release, err := r.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	out := &tail{max: 4 << 10}
	code, err := r.m.box.Exec(ctx, id, sandbox.Cmd{Args: []string{"sh", "-c", pushScript}, Dir: "/workspace/" + ref.Dir(),
		Env: map[string]string{"GH_TOKEN": token, "BRANCH": s.Branch}, Stdout: out, Stderr: out})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("git push failed: %s", clip(strings.TrimSpace(out.String()), 2000))
	}
	base := ref.BaseBranch
	if base == "" {
		if base, err = r.m.scm.DefaultBranch(ctx, ref.Repo); err != nil {
			return "", err
		}
	}
	if title == "" {
		title = s.Title
	}
	url, err := r.m.scm.OpenPR(ctx, PullRequest{Repo: ref.Repo, Head: s.Branch, Base: base, Title: title, Body: body, Draft: *draft})
	if err != nil {
		return "", err
	}
	r.update(func(s *store.Session) {
		s.Repos = slices.Clone(s.Repos) // copy on write: snapshots share the old slice
		s.Repos[i].PRURL = url
	})
	r.emit(r.currentPrompt(), EvArtifact, map[string]string{"kind": "pr", "url": url, "title": title, "repo": ref.Repo})
	if strings.Contains(out.String(), "::dirty") {
		return url, errUncommitted
	}
	return url, nil
}

var errUncommitted = errors.New("the working tree has uncommitted changes that are not in the pull request")

// IsUncommittedWarning reports whether an OpenPR error is only the warning
// that some changes were left out; the pull request itself was opened.
func IsUncommittedWarning(err error) bool { return errors.Is(err, errUncommitted) }

// ----------------------------------------------------------------- helpers

// snapshot copies the session, including its repository list.
func (r *runner) snapshot() store.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.s
	s.Repos = slices.Clone(s.Repos)
	return s
}

func (r *runner) update(fn func(*store.Session)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(&r.s)
	r.save()
}

// save persists the session and announces it. mu held.
func (r *runner) save() {
	if r.gone.Load() {
		return
	}
	if err := r.m.db.SaveSession(context.Background(), &r.s); err != nil {
		r.m.log.Error("save session", "session", r.id, "err", err)
		return
	}
	r.m.publishSession(r.s)
}

func (r *runner) currentPrompt() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turn != nil {
		return r.turn.prompt.ID
	}
	return ""
}

// emit appends an event to the session's log and publishes it.
func (r *runner) emit(promptID, typ string, data any) {
	if r.gone.Load() {
		return
	}
	e := store.Event{Session: r.id, Type: typ, Prompt: promptID, Data: mustJSON(data), At: time.Now()}
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if err := r.m.db.AppendEvent(context.Background(), &e); err != nil {
		r.m.log.Error("append event", "session", r.id, "type", typ, "err", err)
		return
	}
	r.m.hub.Publish(r.id, e)
}

// publish sends an event to live subscribers without storing it.
func (r *runner) publish(promptID, typ string, data any) {
	r.m.hub.Publish(r.id, store.Event{Session: r.id, Type: typ, Prompt: promptID, Data: mustJSON(data), At: time.Now()})
}

// baseEnv is a session's sandbox environment, lowest precedence first: the
// server's environment, the stored credentials, then the session's secrets.
// The server's own variables are added on top by the callers.
func (m *Manager) baseEnv(ctx context.Context, s store.Session, eff Effective) (map[string]string, error) {
	env := maps.Clone(m.cfg.Env)
	if env == nil {
		env = map[string]string{}
	}
	creds, err := m.db.Credentials(ctx)
	if err != nil {
		return nil, err
	}
	for _, k := range AgentCredentials {
		if v := creds[k]; v != "" {
			env[k] = v
		}
	}
	secrets, err := m.db.SecretEnv(ctx, secretTarget(s, eff))
	if err != nil {
		return nil, err
	}
	maps.Copy(env, secrets)
	return env, nil
}

// AgentCredentials are the credentials passed to the agent: either works.
var AgentCredentials = []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}

// turnEnv adds the commit identity: the prompt's author, committed by the server.
func (m *Manager) turnEnv(ctx context.Context, s store.Session, p store.Prompt, eff Effective) (map[string]string, error) {
	env, err := m.baseEnv(ctx, s, eff)
	if err != nil {
		return nil, err
	}
	committer := m.cfg.Committer
	if committer.Name == "" {
		committer = store.Author{Name: "Beethoven", Email: "beethoven@localhost"}
	}
	author := p.Author
	if author.Name == "" || author.Email == "" {
		author = committer
	}
	env["GIT_AUTHOR_NAME"], env["GIT_AUTHOR_EMAIL"] = author.Name, author.Email
	env["GIT_COMMITTER_NAME"], env["GIT_COMMITTER_EMAIL"] = committer.Name, committer.Email
	env["BEETHOVEN_SESSION"] = s.ID
	return env, nil
}

func systemPrompt(s store.Session, eff Effective) string {
	var b strings.Builder
	b.WriteString("You are a background coding agent working unattended in an isolated Linux container. ")
	b.WriteString("Nobody is watching live: do not ask questions; make reasonable assumptions, state them in your final message, and carry the task through to the end.\n")
	if e := eff.Environment; e != nil {
		fmt.Fprintf(&b, "You are working in the %q environment", e.Name)
		if e.Description != "" {
			fmt.Fprintf(&b, ": %s", e.Description)
		}
		b.WriteString(".\n")
	}
	switch len(s.Repos) {
	case 0:
		fmt.Fprintf(&b, "Your workspace is %s, a scratch git repository with no remote.\n", Workdir(s))
	case 1:
		fmt.Fprintf(&b, "Your workspace is %s, a clone of %s, checked out on branch %s.\n", Workdir(s), s.Repos[0].Repo, s.Branch)
	default:
		fmt.Fprintf(&b, "Your workspace is %s. It holds these clones, each checked out on branch %s:\n", Workdir(s), s.Branch)
		for i, ref := range s.Repos {
			fmt.Fprintf(&b, "- /workspace/%s: %s", ref.Dir(), ref.Repo)
			if i == 0 {
				b.WriteString(" (primary)")
			}
			b.WriteString("\n")
		}
	}
	if len(s.Repos) > 0 {
		b.WriteString("Commit your work with git as you go; the commit identity is already configured. ")
		b.WriteString("Do not push or open pull requests with git or gh; call the create_pull_request tool when the work is ready to publish")
		if len(s.Repos) > 1 {
			b.WriteString(", once per repository you changed, naming the repository")
		}
		b.WriteString(".\n")
	}
	b.WriteString("For independent pieces of work that can proceed in parallel, you can start child sessions with spawn_session and follow them with get_session.\n")
	b.WriteString("Finish with a short summary of what you did and anything left undone.")
	if eff.Instructions != "" {
		b.WriteString("\n\n" + eff.Instructions)
	}
	return b.String()
}

func endData(p store.Prompt) map[string]any {
	d := map[string]any{"id": p.ID, "status": p.Status, "error": p.Error, "cost_usd": p.CostUSD}
	if p.StartedAt != nil && p.FinishedAt != nil {
		d["duration_ms"] = p.FinishedAt.Sub(*p.StartedAt).Milliseconds()
	}
	return d
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(map[string]string{"marshal_error": err.Error()})
	}
	return b
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// clip shortens s to at most n bytes without splitting a character.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// tail keeps the last max bytes written to it.
type tail struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = t.buf[over:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.ToValidUTF8(string(t.buf), "")
}

// bootOutput turns boot script output into phase events and live log lines.
type bootOutput struct {
	r       *runner
	phase   string
	partial []byte
	tail    tail
}

func (o *bootOutput) Write(p []byte) (int, error) {
	o.partial = append(o.partial, p...)
	for {
		i := strings.IndexByte(string(o.partial), '\n')
		if i < 0 {
			break
		}
		o.line(string(o.partial[:i]))
		o.partial = o.partial[i+1:]
	}
	return len(p), nil
}

func (o *bootOutput) Flush() {
	if len(o.partial) > 0 {
		o.line(string(o.partial))
		o.partial = nil
	}
}

func (o *bootOutput) line(l string) {
	switch {
	case strings.HasPrefix(l, "::phase "):
		o.phase = strings.TrimPrefix(l, "::phase ")
		if o.phase != "ready" {
			o.r.setSandbox("starting", o.phase, "")
		}
	case strings.HasPrefix(l, "::warn "):
		o.r.emit(o.r.currentPrompt(), EvWarning, map[string]string{"message": strings.TrimPrefix(l, "::warn ")})
	default:
		if o.tail.max == 0 {
			o.tail.max = 8 << 10
		}
		o.tail.Write([]byte(l + "\n"))
		o.r.publish(o.r.currentPrompt(), EvLog, map[string]string{"text": l})
	}
}
