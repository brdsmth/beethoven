package access_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brdsmth/beethoven/internal/access"
	"github.com/brdsmth/beethoven/internal/store"
)

var ctx = context.Background()

// actors covers every kind of reader.
var actors = map[string]access.Actor{
	"server":       access.Server(""),
	"owner":        {AccountID: "own", Role: store.RoleOwner},
	"admin":        {AccountID: "adm", Role: store.RoleAdmin},
	"ann (lead a)": {AccountID: "ann", Role: store.RoleMember, Teams: map[string]string{"a": store.TeamLead}},
	"bob (in a,b)": {AccountID: "bob", Role: store.RoleMember, Teams: map[string]string{"a": "member", "b": "member"}},
	"cy (no team)": {AccountID: "cy", Role: store.RoleMember},
}

// TestListsMatchTheRules checks that the store's SQL predicate selects
// exactly the sessions ReadSession allows, for every combination.
func TestListsMatchTheRules(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"), []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var all []store.Session
	for _, vis := range []string{store.VisibilityWorkspace, store.VisibilityTeam, store.VisibilityPrivate} {
		for _, team := range []string{"", "a", "b"} {
			for _, owner := range []string{"", "ann", "bob", "cy"} {
				if !store.ValidVisibility(vis, team) {
					continue
				}
				s := store.Session{ID: store.NewID(), Title: fmt.Sprintf("%s/%s/%s", vis, team, owner), Branch: "b",
					Source: "test", Status: "idle", Sandbox: "none", Visibility: vis, TeamID: team, AccountID: owner}
				if err := db.CreateSession(ctx, &s); err != nil {
					t.Fatal(err)
				}
				all = append(all, s)
			}
		}
	}
	for name, a := range actors {
		v := a.Viewer()
		list, err := db.Sessions(ctx, store.SessionFilter{Viewer: &v, Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		listed := map[string]bool{}
		for _, s := range list {
			listed[s.ID] = true
		}
		for _, s := range all {
			if want := access.ReadSession(a, s); listed[s.ID] != want {
				t.Errorf("%s, session %s: listed %v, ReadSession %v", name, s.Title, listed[s.ID], want)
			}
		}
	}
}

func TestRules(t *testing.T) {
	ann, bob, cy, admin, owner := actors["ann (lead a)"], actors["bob (in a,b)"], actors["cy (no team)"], actors["admin"], actors["owner"]
	teamA := store.Session{Visibility: store.VisibilityTeam, TeamID: "a", AccountID: "bob"}
	openA := store.Session{Visibility: store.VisibilityWorkspace, TeamID: "a", AccountID: "bob"}
	private := store.Session{Visibility: store.VisibilityPrivate, AccountID: "bob"}
	cases := []struct {
		what string
		got  bool
		want bool
	}{
		{"cy reads a team session of a", access.ReadSession(cy, teamA), false},
		{"cy reads a workspace-visible session of team a", access.ReadSession(cy, openA), true},
		{"cy prompts it: not in the team", access.PromptSession(cy, openA), false},
		{"bob prompts his team's session", access.PromptSession(bob, teamA), true},
		{"ann (lead) archives bob's team session", access.ManageSession(ann, teamA), true},
		{"bob manages his own session", access.ManageSession(bob, teamA), true},
		{"admin prompts a team session", access.PromptSession(admin, teamA), true},
		{"admin reads bob's private session", access.ReadSession(admin, private), false},
		{"owner reads bob's private session", access.ReadSession(owner, private), true},
		{"owner prompts bob's private session", access.PromptSession(owner, private), false},
		{"bob prompts his private session", access.PromptSession(bob, private), true},
		{"cy uses a workspace project", access.UseEnvironment(cy, store.Environment{}), true},
		{"cy uses team a's project", access.UseEnvironment(cy, store.Environment{TeamID: "a"}), false},
		{"bob manages team a's project", access.ManageEnvironment(bob, store.Environment{TeamID: "a"}), true},
		{"bob writes team a's secrets (not a lead)", access.WriteScope(bob, store.TeamScope("a"), nil), false},
		{"ann writes team a's secrets", access.WriteScope(ann, store.TeamScope("a"), nil), true},
		{"bob reads team b's secret names", access.ReadScope(bob, store.TeamScope("b"), nil), true},
		{"cy writes global secrets", access.WriteScope(cy, "", nil), false},
		{"admin writes repository secrets", access.WriteScope(admin, "acme/app", nil), true},
		{"cy joins an open team", access.JoinTeam(cy, store.Team{JoinPolicy: store.JoinOpen}), true},
		{"cy joins an invite-only team", access.JoinTeam(cy, store.Team{JoinPolicy: store.JoinInvite}), false},
		{"the server, as nobody, can't join", access.JoinTeam(access.Server(""), store.Team{JoinPolicy: store.JoinOpen}), false},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: %v, want %v", c.what, c.got, c.want)
		}
	}
	if a, ok := access.From(access.With(ctx, bob)); !ok || a.AccountID != "bob" {
		t.Errorf("actor from context = %+v, %v", a, ok)
	}
}
