package session

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/brdsmth/beethoven/internal/capability"
	"github.com/brdsmth/beethoven/internal/sandbox"
	"github.com/brdsmth/beethoven/internal/store"
)

// Capabilities reach a sandbox when it boots: the server sends the catalog
// on the boot script's stdin, and the ids it enabled (the global settings',
// the environment's and those added during the session) in CAPABILITIES;
// the sandbox adds the ids its repositories name or look like they need and
// installs them all. What each repository needed is kept (repo_capabilities)
// for sizing its next sandbox and unpacking cached installs into it.
// What it set up comes back as "::capability" lines and is logged as an
// EvCapabilities event, so the session's log, not a table, remembers it.

//go:embed capabilities.sh
var capabilityScript string

// capabilityState is what a session's sandbox has: the capabilities set up,
// those its repositories suggest, and the variables they set for the agent.
type capabilityState struct {
	Enabled   []string          `json:"enabled"`
	Suggested []string          `json:"suggested"`
	Env       map[string]string `json:"env,omitempty"`
}

// same reports whether two states hold the same capabilities, suggestions
// and variables, in any order.
func (s capabilityState) same(o capabilityState) bool {
	sorted := func(l []string) []string { return slices.Sorted(slices.Values(l)) }
	return slices.Equal(sorted(s.Enabled), sorted(o.Enabled)) && slices.Equal(sorted(s.Suggested), sorted(o.Suggested)) &&
		maps.Equal(s.Env, o.Env)
}

// capabilityLines reads the "::capability", "::suggest", "::repo",
// "::cache", "::env" and "::warn" lines of the capability scripts.
type capabilityLines struct {
	state    capabilityState
	warnings []string
	recorded string
	repos    map[string][]string // by repository directory
	exports  []capabilityExport
}

func (c *capabilityLines) line(l string) bool {
	cmd, arg, _ := strings.Cut(l, " ")
	switch cmd {
	case "::capability":
		c.state.Enabled = capability.Union(c.state.Enabled, []string{arg})
	case "::suggest":
		c.state.Suggested = capability.Union(c.state.Suggested, []string{arg})
	case "::env":
		if k, v, ok := strings.Cut(arg, "="); ok && declaredEnv(k) {
			if c.state.Env == nil {
				c.state.Env = map[string]string{}
			}
			c.state.Env[k] = v
		}
	case "::recorded":
		c.recorded = arg
	case "::repo":
		if dir, id, ok := strings.Cut(arg, " "); ok && len(capability.Unknown([]string{id})) == 0 {
			if c.repos == nil {
				c.repos = map[string][]string{}
			}
			c.repos[dir] = capability.Union(c.repos[dir], []string{id})
		}
	case "::cache":
		if f := strings.Fields(arg); len(f) == 5 {
			c.exports = append(c.exports, capabilityExport{ID: f[0], Hash: f[1], Machine: f[2], SHA256: f[3], File: f[4]})
		}
	case "::warn":
		c.warnings = append(c.warnings, arg)
	default:
		return false
	}
	return true
}

// report is the state to log, with lists rather than nulls.
func (c *capabilityLines) report() capabilityState {
	s := c.state
	if s.Enabled == nil {
		s.Enabled = []string{}
	}
	s.Suggested = slices.DeleteFunc(slices.Clone(s.Suggested), func(id string) bool { return slices.Contains(s.Enabled, id) })
	if s.Suggested == nil {
		s.Suggested = []string{}
	}
	return s
}

// declaredEnv reports whether some capability sets k: a sandbox can't set
// anything else for the agent.
func declaredEnv(k string) bool {
	for _, c := range capability.Catalog {
		if _, ok := c.Env[k]; ok {
			return true
		}
	}
	return false
}

// loggedCapabilities reads a session's log: what its last boot set up, plus
// what was added since, and every capability added during the session.
func (m *Manager) loggedCapabilities(ctx context.Context, sessionID string) (capabilityState, []string, error) {
	var state capabilityState
	var added []string
	events, err := m.db.EventsOfType(ctx, sessionID, EvCapabilities, EvCapability)
	if err != nil {
		return state, nil, err
	}
	env := map[string]string{}
	for _, e := range events {
		var d struct {
			capabilityState
			ID string `json:"id"`
		}
		if json.Unmarshal(e.Data, &d) != nil {
			continue
		}
		if e.Type == EvCapabilities {
			state.Enabled, state.Suggested = d.Enabled, d.Suggested
		} else {
			state.Enabled = capability.Union(state.Enabled, []string{d.ID})
			added = capability.Union(added, []string{d.ID})
		}
		for k, v := range d.Env {
			env[k] = v
		}
	}
	state.Env = env
	return state, added, nil
}

// enabledCapabilities are the ids the server asks a session's sandbox to
// set up: the global settings' and environment's, then those added during
// the session.
func (m *Manager) enabledCapabilities(ctx context.Context, s store.Session, eff Effective) ([]string, error) {
	_, added, err := m.loggedCapabilities(ctx, s.ID)
	return capability.Union(eff.Capabilities, added), err
}

// capabilityEnv adds what the session's capabilities set for the agent,
// without replacing variables already set (secrets win).
func (m *Manager) capabilityEnv(ctx context.Context, sessionID string, env map[string]string) error {
	state, _, err := m.loggedCapabilities(ctx, sessionID)
	if err != nil {
		return err
	}
	for k, v := range state.Env {
		if _, set := env[k]; !set && declaredEnv(k) {
			env[k] = v
		}
	}
	return nil
}

// capabilityRepos is REPOS for the capability scripts, which only need
// each repository's directory.
func capabilityRepos(s store.Session) string {
	lines := make([]string, len(s.Repos))
	for i, ref := range s.Repos {
		lines[i] = ref.Dir() + " - -"
	}
	return strings.Join(lines, "\n")
}

// AddedCapability is what adding a capability to a session did.
type AddedCapability struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Kept     string   `json:"kept"`            // where it was kept for later: environment, repository or session
	Where    string   `json:"where,omitempty"` // the environment, or the file that records it
	Warnings []string `json:"warnings,omitempty"`
}

// AddCapability installs a capability in a session's sandbox now and keeps
// it for later: in the session's environment if it has one, otherwise in
// its primary repository's .beethoven/capabilities (uncommitted), and in
// any case for the rest of the session. by is who asked: agent or web.
func (m *Manager) AddCapability(ctx context.Context, sessionID, id, by string) (AddedCapability, error) {
	c, ok := capability.Get(id)
	if !ok {
		ids := make([]string, len(capability.Catalog))
		for i, x := range capability.Catalog {
			ids[i] = x.ID
		}
		return AddedCapability{}, fmt.Errorf("%w: no capability %q; there are %s", ErrInvalid, id, strings.Join(ids, ", "))
	}
	r, err := m.runner(ctx, sessionID)
	if err != nil {
		return AddedCapability{}, err
	}
	return r.addCapability(ctx, c, by)
}

func (r *runner) addCapability(ctx context.Context, c capability.Capability, by string) (AddedCapability, error) {
	box, release, err := r.acquire(ctx)
	if err != nil {
		return AddedCapability{}, err
	}
	defer release()
	s := r.snapshot()
	eff, err := r.m.Effective(ctx, s.EnvironmentID)
	if err != nil {
		return AddedCapability{}, err
	}
	env, err := r.m.baseEnv(ctx, s, eff)
	if err != nil {
		return AddedCapability{}, err
	}
	env["CAPABILITIES"], env["WORKDIR"], env["REPOS"] = c.ID, Workdir(s), capabilityRepos(s)
	added := AddedCapability{ID: c.ID, Name: c.Name, Kept: "session"}
	switch {
	case eff.Environment != nil:
		added.Kept, added.Where = "environment", eff.Environment.Name
	case len(s.Repos) > 0:
		env["CAP_RECORD"] = "/workspace/" + s.Repos[0].Dir() + "/.beethoven/capabilities"
		added.Kept, added.Where = "repository", s.Repos[0].Repo+": .beethoven/capabilities"
	}
	out := &capabilityOutput{}
	code, err := r.m.box.Exec(ctx, box, sandbox.Cmd{Args: []string{"sh", "-c", capabilityScript + "\ncap_unpack\ncap_add\n"},
		Env: env, Stdin: bytes.NewReader(capability.Archive()), Stdout: out, Stderr: out})
	out.Flush()
	if err != nil {
		return added, err
	}
	added.Warnings = out.warnings
	if code != 0 || !slices.Contains(out.state.Enabled, c.ID) {
		why := strings.Join(out.warnings, "; ")
		if why == "" {
			why = clip(strings.TrimSpace(out.tail.String()), 2000)
		}
		return added, fmt.Errorf("installing %s failed: %s", c.Name, why)
	}
	if added.Kept == "environment" {
		// Fresh from the store: the environment may have changed meanwhile.
		e, err := r.m.db.Environment(ctx, eff.Environment.ID)
		if err == nil && !slices.Contains(e.Overrides.Capabilities, c.ID) {
			e.Overrides.Capabilities = append(e.Overrides.Capabilities, c.ID)
			err = r.m.db.SaveEnvironment(ctx, &e)
		}
		if err != nil {
			return added, fmt.Errorf("installed %s, but couldn't keep it in the environment: %w", c.Name, err)
		}
	}
	r.emit(r.currentPrompt(), EvCapability, map[string]any{"id": c.ID, "name": c.Name, "by": by, "kept": added.Kept,
		"where": added.Where, "env": out.state.Env})
	return added, nil
}

// SessionCapabilities says which capabilities a session has and which its
// repositories look like they need.
type SessionCapabilities struct {
	Enabled    []string `json:"enabled"`
	Suggested  []string `json:"suggested"`
	SecretsSet []string `json:"secrets_set"` // the capabilities' secrets the session has
}

// Capabilities reports a session's capabilities, asking its sandbox (which
// it starts if need be) what its repositories suggest.
func (m *Manager) Capabilities(ctx context.Context, sessionID string) (SessionCapabilities, error) {
	r, err := m.runner(ctx, sessionID)
	if err != nil {
		return SessionCapabilities{}, err
	}
	box, release, err := r.acquire(ctx)
	if err != nil {
		return SessionCapabilities{}, err
	}
	defer release()
	s := r.snapshot()
	eff, err := m.Effective(ctx, s.EnvironmentID)
	if err != nil {
		return SessionCapabilities{}, err
	}
	state, added, err := m.loggedCapabilities(ctx, s.ID)
	if err != nil {
		return SessionCapabilities{}, err
	}
	out := &capabilityOutput{}
	_, err = m.box.Exec(ctx, box, sandbox.Cmd{Args: []string{"sh", "-c", capabilityScript + "\ncap_unpack\ncap_detect\n"},
		Env: map[string]string{"WORKDIR": Workdir(s), "REPOS": capabilityRepos(s)}, Stdin: bytes.NewReader(capability.Archive()),
		Stdout: out, Stderr: out})
	out.Flush()
	if err != nil {
		return SessionCapabilities{}, err
	}
	lines := capabilityLines{state: capabilityState{Enabled: capability.Union(eff.Capabilities, added, state.Enabled),
		Suggested: out.state.Suggested}}
	rep := lines.report()
	env, err := m.baseEnv(ctx, s, eff)
	if err != nil {
		return SessionCapabilities{}, err
	}
	set := []string{}
	for _, c := range capability.Catalog {
		for _, sec := range c.Secrets {
			if env[sec.Name] != "" && !slices.Contains(set, sec.Name) {
				set = append(set, sec.Name)
			}
		}
	}
	return SessionCapabilities{Enabled: rep.Enabled, Suggested: rep.Suggested, SecretsSet: set}, nil
}

// capabilityOutput collects the capability scripts' output.
type capabilityOutput struct {
	capabilityLines
	partial []byte
	tail    tail
}

func (o *capabilityOutput) Write(p []byte) (int, error) {
	o.partial = append(o.partial, p...)
	for {
		i := bytes.IndexByte(o.partial, '\n')
		if i < 0 {
			return len(p), nil
		}
		o.take(string(o.partial[:i]))
		o.partial = o.partial[i+1:]
	}
}

func (o *capabilityOutput) Flush() {
	if len(o.partial) > 0 {
		o.take(string(o.partial))
		o.partial = nil
	}
}

func (o *capabilityOutput) take(l string) {
	if strings.HasPrefix(l, "::phase ") || o.line(l) {
		return
	}
	if o.tail.max == 0 {
		o.tail.max = 8 << 10
	}
	o.tail.Write([]byte(l + "\n"))
}
