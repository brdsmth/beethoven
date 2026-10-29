package session_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

func TestPluginsReachTheBoot(t *testing.T) {
	env := fake.NewEnv(t)
	env.DB.SaveSettings(ctx, store.Settings{Overrides: store.Overrides{Plugins: []string{"compound-engineering@EveryInc/compound-engineering-plugin"}}})
	e := environment(t, env, store.Environment{Name: "Web", Overrides: store.Overrides{
		Plugins: []string{"review@https://example.com/marketplace.git", "compound-engineering@EveryInc/compound-engineering-plugin"}}})

	_, p := submit(t, env, "one")
	waitStatus(t, env, p.ID, store.Done)
	_, p2 := submit(t, env, "two", func(r *session.Request) { r.EnvironmentID = e.ID })
	waitStatus(t, env, p2.ID, store.Done)
	boots := env.Box.Boots
	if got := boots[0].Env["PLUGINS"]; got != "compound-engineering@EveryInc/compound-engineering-plugin" {
		t.Errorf("global PLUGINS = %q", got)
	}
	if got := boots[1].Env["PLUGINS"]; got != "compound-engineering@EveryInc/compound-engineering-plugin\nreview@https://example.com/marketplace.git" {
		t.Errorf("environment PLUGINS = %q; want the global ones first, once", got)
	}
}

func TestValidatePlugins(t *testing.T) {
	for _, ok := range []string{"compound-engineering@EveryInc/compound-engineering-plugin", "x@https://example.com/m.git", "x@git@github.com:a/b.git"} {
		if err := session.ValidateOverrides(store.Overrides{Plugins: []string{ok}}); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "no-marketplace", "@a/b", "x@", "x@-flag", "x@a/b c", "a b@c/d", "-x@a/b"} {
		if err := session.ValidateOverrides(store.Overrides{Plugins: []string{bad}}); !errors.Is(err, session.ErrInvalid) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
	many := make([]string, session.MaxPlugins+1)
	for i := range many {
		many[i] = "p@a/b"
	}
	if err := session.ValidateOverrides(store.Overrides{Plugins: many}); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("too many: err = %v", err)
	}
	if got := (store.Overrides{Plugins: []string{"b@x/y", "a@x/y"}}).Over(store.Overrides{Plugins: []string{"a@x/y"}}); !slices.Equal(got.Plugins, []string{"a@x/y", "b@x/y"}) {
		t.Errorf("environment over global = %v", got.Plugins)
	}
}
