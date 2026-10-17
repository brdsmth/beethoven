package session_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

// capabilityEvents decodes a session's capability events.
func capabilityEvents(t *testing.T, env *fake.Env, sessionID, typ string) []map[string]any {
	t.Helper()
	evs, err := env.DB.EventsOfType(ctx, sessionID, typ)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, e := range evs {
		var d map[string]any
		json.Unmarshal(e.Data, &d)
		out = append(out, d)
	}
	return out
}

func strs(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestCapabilitiesReachTheSandbox(t *testing.T) {
	env := fake.NewEnv(t)
	env.Box.RepoCapabilities = []string{"rust", "expo"} // named in a repository's .beethoven/capabilities
	env.Box.Suggest = []string{"python", "go"}
	if err := env.DB.SaveSettings(ctx, store.Settings{Overrides: store.Overrides{Capabilities: []string{"go"}, CPUs: 1, MemoryMB: 1024}}); err != nil {
		t.Fatal(err)
	}
	e := store.Environment{ID: store.NewID(), Name: "mobile", InheritSecrets: true, Overrides: store.Overrides{Capabilities: []string{"expo", "go"}}}
	if err := env.DB.CreateEnvironment(ctx, &e); err != nil {
		t.Fatal(err)
	}
	s, p := submit(t, env, "build it", func(r *session.Request) { r.EnvironmentID = e.ID })
	waitStatus(t, env, p.ID, store.Done)

	if boot := env.Box.Boots[0]; boot.Env["CAPABILITIES"] != "go expo" || boot.Stdin == nil {
		t.Errorf("boot got CAPABILITIES=%q and stdin %v; want the global and environment ones, and the catalog",
			boot.Env["CAPABILITIES"], boot.Stdin != nil)
	}
	if spec := env.Box.Specs[0]; spec.CPUs != 2 || spec.MemoryMB != 4096 {
		t.Errorf("sandbox created with %v CPUs and %d MB; expo needs 2 and 4096", spec.CPUs, spec.MemoryMB)
	}
	evs := capabilityEvents(t, env, s.ID, session.EvCapabilities)
	if len(evs) != 1 || !slices.Equal(strs(evs[0]["enabled"]), []string{"go", "expo", "rust"}) ||
		!slices.Equal(strs(evs[0]["suggested"]), []string{"python"}) {
		t.Fatalf("capability events = %v", evs)
	}

	// A sandbox that boots again with the same capabilities logs nothing new.
	env.Box.Stop(ctx, "beethoven-"+s.ID)
	p2, _ := env.M.Enqueue(ctx, s.ID, session.PromptInput{Content: "again", Source: "test"})
	waitStatus(t, env, p2.ID, store.Done)
	if n := len(env.Box.Boots); n != 2 {
		t.Fatalf("boots = %d", n)
	}
	if evs := capabilityEvents(t, env, s.ID, session.EvCapabilities); len(evs) != 1 {
		t.Errorf("an unchanged boot logged again: %v", evs)
	}
}

func TestAgentAddsCapabilities(t *testing.T) {
	env := fake.NewEnv(t)
	s, p := submit(t, env, "one", func(r *session.Request) { r.Repos = []store.RepoRef{{Repo: "acme/app"}} })
	waitStatus(t, env, p.ID, store.Done)

	if _, err := env.M.AddCapability(ctx, s.ID, "cobol", "agent"); !errors.Is(err, session.ErrInvalid) || !strings.Contains(err.Error(), "expo") {
		t.Errorf("unknown capability: %v", err)
	}
	env.Box.CapabilityErr = map[string]bool{"rust": true}
	if _, err := env.M.AddCapability(ctx, s.ID, "rust", "agent"); err == nil || !strings.Contains(err.Error(), "failed to install") {
		t.Errorf("failed install: %v", err)
	}

	added, err := env.M.AddCapability(ctx, s.ID, "android", "agent")
	if err != nil {
		t.Fatal(err)
	}
	record := "/workspace/app/.beethoven/capabilities"
	if added.Kept != "repository" || added.Where != "acme/app: .beethoven/capabilities" {
		t.Errorf("added = %+v", added)
	}
	if b, _ := env.Box.File(record); string(b) != "android\n" {
		t.Errorf("%s = %q", record, b)
	}
	evs := capabilityEvents(t, env, s.ID, session.EvCapability)
	if len(evs) != 1 || evs[0]["id"] != "android" || evs[0]["by"] != "agent" || evs[0]["kept"] != "repository" {
		t.Errorf("events = %v", evs)
	}

	// Later turns get what it set for the agent, and a new sandbox installs it again.
	p2, _ := env.M.Enqueue(ctx, s.ID, session.PromptInput{Content: "two", Source: "test"})
	waitStatus(t, env, p2.ID, store.Done)
	if got := env.Box.Turns[1].Env["ANDROID_HOME"]; got != "/home/agent/android-sdk" {
		t.Errorf("ANDROID_HOME = %q", got)
	}
	env.Box.Destroy(ctx, "beethoven-"+s.ID)
	p3, _ := env.M.Enqueue(ctx, s.ID, session.PromptInput{Content: "three", Source: "test"})
	waitStatus(t, env, p3.ID, store.Done)
	if got := env.Box.Boots[len(env.Box.Boots)-1].Env["CAPABILITIES"]; got != "android" {
		t.Errorf("the next sandbox got CAPABILITIES=%q", got)
	}
	if spec := env.Box.Specs[len(env.Box.Specs)-1]; spec.CPUs != 0 {
		t.Errorf("CPUs = %v; without a limit configured the provider's default stays", spec.CPUs)
	}

	have, err := env.M.Capabilities(ctx, s.ID)
	if err != nil || !slices.Equal(have.Enabled, []string{"android"}) {
		t.Errorf("capabilities = %+v, %v", have, err)
	}
}

func TestCapabilitiesAddedToAnEnvironmentStayThere(t *testing.T) {
	env := fake.NewEnv(t)
	e := store.Environment{ID: store.NewID(), Name: "app", InheritSecrets: true, Repos: []store.RepoRef{{Repo: "acme/app"}}}
	if err := env.DB.CreateEnvironment(ctx, &e); err != nil {
		t.Fatal(err)
	}
	s, p := submit(t, env, "one", func(r *session.Request) { r.EnvironmentID = e.ID })
	waitStatus(t, env, p.ID, store.Done)
	added, err := env.M.AddCapability(ctx, s.ID, "expo", "web")
	if err != nil || added.Kept != "environment" || added.Where != "app" {
		t.Fatalf("added = %+v, %v", added, err)
	}
	if got, _ := env.DB.Environment(ctx, e.ID); !slices.Equal(got.Overrides.Capabilities, []string{"expo"}) {
		t.Errorf("environment capabilities = %v", got.Overrides.Capabilities)
	}
	if run := env.Box.CapabilityRuns[0]; run.Env["CAP_RECORD"] != "" {
		t.Errorf("recorded in a repository too: %q", run.Env["CAP_RECORD"])
	}
	// A new session in the environment gets it from the start.
	_, p2 := submit(t, env, "two", func(r *session.Request) { r.EnvironmentID = e.ID })
	waitStatus(t, env, p2.ID, store.Done)
	if boots := env.Box.Boots; len(boots) != 2 || boots[1].Env["CAPABILITIES"] != "expo" {
		t.Errorf("the environment's next session didn't get expo")
	}
}

func TestUnknownCapabilitiesAreRefused(t *testing.T) {
	if err := session.ValidateOverrides(store.Overrides{Capabilities: []string{"expo", "cobol"}}); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("err = %v", err)
	}
	if err := session.ValidateOverrides(store.Overrides{Capabilities: []string{"expo", "android"}}); err != nil {
		t.Error(err)
	}
	if got := (store.Overrides{Capabilities: []string{"expo", "go"}}).Over(store.Overrides{Capabilities: []string{"go", "rust"}}); !slices.Equal(got.Capabilities, []string{"go", "rust", "expo"}) {
		t.Errorf("environment over global = %v; want both, global first", got.Capabilities)
	}
}
