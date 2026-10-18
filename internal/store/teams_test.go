package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPeople(t *testing.T) {
	s := open(t)
	owner, err := s.CreateOwner(ctx, Account{Email: "o@x.dev", Name: "Olive"}, "hash")
	if err != nil || owner.Role != RoleOwner || !owner.Owner {
		t.Fatalf("owner = %+v, %v", owner, err)
	}

	// An invitation creates an account once, with its role.
	in := Invite{Role: RoleAdmin, CreatedBy: owner.ID}
	token, err := s.CreateInvite(ctx, &in)
	if err != nil || token == "" || len(in.ID) != 16 {
		t.Fatalf("invite %+v, %q, %v", in, token, err)
	}
	if got, err := s.Invite(ctx, token); err != nil || got.ID != in.ID || got.Role != RoleAdmin {
		t.Errorf("open invite = %+v, %v", got, err)
	}
	admin, err := s.AcceptInvite(ctx, token, Account{Email: "a@x.dev", Name: "Ada"}, "hash")
	if err != nil || admin.Role != RoleAdmin {
		t.Fatalf("accepted = %+v, %v", admin, err)
	}
	if _, err := s.AcceptInvite(ctx, token, Account{Email: "b@x.dev"}, "hash"); !errors.Is(err, ErrNotFound) {
		t.Errorf("used an invitation twice: %v", err)
	}
	named := Invite{Role: RoleMember, Email: "m@x.dev", CreatedBy: owner.ID}
	tok2, _ := s.CreateInvite(ctx, &named)
	if _, err := s.AcceptInvite(ctx, tok2, Account{Email: "else@x.dev"}, "hash"); !errors.Is(err, ErrInvalid) {
		t.Errorf("accepted someone else's invitation: %v", err)
	}
	if _, err := s.AcceptInvite(ctx, tok2, Account{Email: "o@x.dev"}, "hash"); !errors.Is(err, ErrInvalid) {
		// the invitation was for m@x.dev; and o@x.dev exists anyway
		t.Errorf("accepted for a taken email: %v", err)
	}
	if list, _ := s.Invites(ctx); len(list) != 1 || list[0].ID != named.ID {
		t.Errorf("open invites = %+v", list)
	}
	s.DeleteInvite(ctx, named.ID)
	if list, _ := s.Invites(ctx); len(list) != 0 {
		t.Errorf("withdrawn invite still open: %+v", list)
	}

	// There's always an active owner.
	if _, err := s.UpdateAccount(ctx, owner.ID, RoleAdmin, false); !errors.Is(err, ErrLastOwner) {
		t.Errorf("demoted the last owner: %v", err)
	}
	if _, err := s.UpdateAccount(ctx, owner.ID, RoleOwner, true); !errors.Is(err, ErrLastOwner) {
		t.Errorf("deactivated the last owner: %v", err)
	}
	if _, err := s.UpdateAccount(ctx, admin.ID, RoleOwner, false); err != nil {
		t.Fatal(err)
	}
	if a, err := s.UpdateAccount(ctx, owner.ID, RoleMember, false); err != nil || a.Role != RoleMember {
		t.Errorf("with another owner, demoting: %+v, %v", a, err)
	}
	if first, _ := s.FirstOwner(ctx); first.ID != admin.ID {
		t.Errorf("first owner = %+v", first)
	}

	// Deactivating signs out and stops tokens.
	s.CreateLogin(ctx, "login", owner.ID, ownerExpiry())
	_, value, _ := s.CreateAPIToken(ctx, owner.ID, "cli")
	if a, err := s.APITokenAccount(ctx, value); err != nil || a.ID != owner.ID {
		t.Fatalf("token account = %+v, %v", a, err)
	}
	if list, _ := s.APITokens(ctx, owner.ID); len(list) != 1 || list[0].LastUsedAt == nil || list[0].Hint != value[len(value)-4:] {
		t.Errorf("tokens = %+v", list)
	}
	s.UpdateAccount(ctx, owner.ID, RoleMember, true)
	if _, err := s.LoginAccount(ctx, "login"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a deactivated account is still signed in: %v", err)
	}
	if _, err := s.APITokenAccount(ctx, value); !errors.Is(err, ErrNotFound) {
		t.Errorf("a deactivated account's token works: %v", err)
	}
	if list, _ := s.Accounts(ctx); len(list) != 2 || list[0].ID != admin.ID || !list[1].Disabled {
		t.Errorf("accounts = %+v", list)
	}
}

func TestTeams(t *testing.T) {
	s := open(t)
	owner, _ := s.CreateOwner(ctx, Account{Email: "o@x.dev"}, "hash")
	inv := Invite{Role: RoleMember, CreatedBy: owner.ID}
	tok, _ := s.CreateInvite(ctx, &inv)
	bob, _ := s.AcceptInvite(ctx, tok, Account{Email: "b@x.dev", Name: "Bob"}, "hash")

	web := Team{Name: "Web Platform"}
	if err := s.CreateTeam(ctx, &web, owner.ID); err != nil || web.Slug != "web-platform" || web.Members != 1 ||
		web.JoinPolicy != JoinInvite || web.DefaultVisibility != VisibilityTeam {
		t.Fatalf("team = %+v, %v", web, err)
	}
	if err := s.CreateTeam(ctx, &Team{Name: "web platform"}, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("two teams with one slug: %v", err)
	}
	for _, bad := range []Team{{Name: ""}, {Name: "x", Slug: "Not A Slug"}, {Name: "x", Color: "red"}, {Name: "x", JoinPolicy: "maybe"}} {
		if err := s.CreateTeam(ctx, &bad, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("created %+v: %v", bad, err)
		}
	}

	if err := s.SetTeamMember(ctx, web.ID, bob.ID, TeamMemberRole); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTeamMember(ctx, web.ID, "nobody", TeamMemberRole); !errors.Is(err, ErrNotFound) {
		t.Errorf("added a missing account: %v", err)
	}
	if err := s.RemoveTeamMember(ctx, web.ID, owner.ID); !errors.Is(err, ErrLastLead) {
		t.Errorf("removed the last lead: %v", err)
	}
	if err := s.SetTeamMember(ctx, web.ID, owner.ID, TeamMemberRole); !errors.Is(err, ErrLastLead) {
		t.Errorf("demoted the last lead: %v", err)
	}
	s.SetTeamMember(ctx, web.ID, bob.ID, TeamLead)
	if err := s.RemoveTeamMember(ctx, web.ID, owner.ID); err != nil {
		t.Errorf("with another lead: %v", err)
	}
	members, _ := s.TeamMembers(ctx, web.ID)
	if len(members) != 1 || members[0].ID != bob.ID || members[0].TeamRole != TeamLead {
		t.Errorf("members = %+v", members)
	}
	if m, _ := s.Memberships(ctx, bob.ID); m[web.ID] != TeamLead {
		t.Errorf("bob's memberships = %v", m)
	}

	// A default team only counts while one is a member.
	s.SetDefaultTeam(ctx, bob.ID, web.ID)
	if id, _ := s.DefaultTeam(ctx, bob.ID); id != web.ID {
		t.Errorf("default team = %q", id)
	}
	s.SetDefaultTeam(ctx, owner.ID, web.ID)
	if id, _ := s.DefaultTeam(ctx, owner.ID); id != "" {
		t.Errorf("default team of a non-member = %q", id)
	}

	// An invitation can bring someone into a team.
	inv2 := Invite{Role: RoleMember, TeamID: web.ID, CreatedBy: owner.ID}
	tok2, _ := s.CreateInvite(ctx, &inv2)
	cy, _ := s.AcceptInvite(ctx, tok2, Account{Email: "c@x.dev"}, "hash")
	if m, _ := s.Memberships(ctx, cy.ID); m[web.ID] != TeamMemberRole {
		t.Errorf("invited into the team: %v", m)
	}

	if err := s.ArchiveTeam(ctx, web.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.TeamBySlug(ctx, "web-platform"); got.ArchivedAt == nil || got.Members != 2 {
		t.Errorf("archived team = %+v", got)
	}

	// Projects are unique by name per owner.
	if err := s.CreateEnvironment(ctx, &Environment{ID: NewID(), Name: "App"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateEnvironment(ctx, &Environment{ID: NewID(), Name: "app", TeamID: web.ID}); err != nil {
		t.Errorf("a team's project named like the workspace's: %v", err)
	}
	if err := s.CreateEnvironment(ctx, &Environment{ID: NewID(), Name: "APP", TeamID: web.ID}); !errors.Is(err, ErrConflict) {
		t.Errorf("two projects of one team with one name: %v", err)
	}

	// Team secrets sit between global and project secrets.
	layers := SecretTarget{TeamID: web.ID, EnvironmentID: "e1", InheritGlobal: true}.Layers()
	if len(layers) != 3 || layers[0] != "" || layers[1] != TeamScope(web.ID) || layers[2] != EnvScope("e1") {
		t.Errorf("layers = %q", layers)
	}

	s.Audit(ctx, AuditEvent{ActorID: owner.ID, Actor: "o", TeamID: web.ID, Action: "team.created", Target: web.ID}, map[string]string{"name": web.Name})
	s.Audit(ctx, AuditEvent{Action: "account.updated", Target: bob.ID}, nil)
	if all, _ := s.AuditEvents(ctx, "", 0, 0); len(all) != 2 || all[0].Action != "account.updated" {
		t.Errorf("audit = %+v", all)
	}
	if mine, _ := s.AuditEvents(ctx, web.ID, 0, 0); len(mine) != 1 || string(mine[0].Detail) != `{"name":"Web Platform"}` {
		t.Errorf("team audit = %+v", mine)
	}
}

func TestSessionVisibility(t *testing.T) {
	s := open(t)
	mk := func(vis, team, account string) Session {
		x := Session{ID: NewID(), Title: vis + team + account, Branch: "b", Source: "test", Status: "idle", Sandbox: "none",
			Visibility: vis, TeamID: team, AccountID: account}
		if err := s.CreateSession(ctx, &x); err != nil {
			t.Fatal(err)
		}
		return x
	}
	if err := s.CreateSession(ctx, &Session{ID: NewID(), Branch: "b", Source: "t", Visibility: VisibilityTeam}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a team-visible session without a team: %v", err)
	}
	open := mk(VisibilityWorkspace, "", "")
	teamA := mk(VisibilityTeam, "a", "ann")
	teamB := mk(VisibilityTeam, "b", "bob")
	mine := mk(VisibilityPrivate, "", "ann")
	theirs := mk(VisibilityPrivate, "a", "bob")
	ids := func(v Viewer, f SessionFilter) map[string]bool {
		f.Viewer = &v
		list, err := s.Sessions(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, x := range list {
			out[x.ID] = true
		}
		return out
	}
	ann := Viewer{AccountID: "ann", Teams: []string{"a"}}
	if got := ids(ann, SessionFilter{}); len(got) != 3 || !got[open.ID] || !got[teamA.ID] || !got[mine.ID] {
		t.Errorf("ann sees %v", got)
	}
	if got := ids(Viewer{AccountID: "cy"}, SessionFilter{}); len(got) != 1 || !got[open.ID] {
		t.Errorf("someone in no team sees %v", got)
	}
	if got := ids(Viewer{AccountID: "ad", Admin: true}, SessionFilter{}); len(got) != 3 || got[mine.ID] || got[theirs.ID] {
		t.Errorf("an admin sees %v; private sessions are the owner's", got)
	}
	if got := ids(Viewer{AccountID: "ow", Admin: true, Owner: true}, SessionFilter{}); len(got) != 5 {
		t.Errorf("an owner sees %v", got)
	}
	if got := ids(ann, SessionFilter{Teams: []string{"a"}}); len(got) != 1 || !got[teamA.ID] {
		t.Errorf("ann's team a: %v", got)
	}
	if got := ids(ann, SessionFilter{Teams: []string{""}}); len(got) != 2 || !got[open.ID] || !got[mine.ID] {
		t.Errorf("ann's workspace sessions: %v", got)
	}
	if got := ids(ann, SessionFilter{Account: "ann"}); len(got) != 2 || !got[teamA.ID] || !got[mine.ID] {
		t.Errorf("ann's own: %v", got)
	}
	_ = teamB
}

func ownerExpiry() time.Time { return time.Now().Add(time.Hour) }

// TestMigratingKeepsData upgrades a database made before people and teams:
// the owner keeps owning, projects and GitHub connections carry over, and
// sessions stay visible to everyone.
func TestMigratingKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	names, _ := fs.Glob(migrations, "migrations/*.sql")
	for i, name := range names[:7] { // up to attachments
		body, _ := migrations.ReadFile(name)
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1))
	}
	for _, q := range []string{
		`INSERT INTO accounts (id, email, name, password_hash, owner, created_at) VALUES ('own', 'o@x.dev', 'O', 'h', 1, 1)`,
		`INSERT INTO environments (id, name, repos, created_at, updated_at) VALUES ('e1', 'Web', '[{"repo":"acme/web"}]', 1, 1)`,
		`INSERT INTO environments (id, name, created_at, updated_at) VALUES ('e2', 'api', 1, 1)`,
		`INSERT INTO github_accounts (app_id, id, login, tokens, connected_at, updated_at) VALUES (1, 7, 'octo', x'00', 1, 1)`,
		`INSERT INTO sessions (id, title, branch, source, created_at, updated_at) VALUES ('s1', 't', 'b', 'web', 1, 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()

	s, err := Open(path, []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if a, _ := s.Account(ctx, "own"); a.Role != RoleOwner || !a.Owner {
		t.Errorf("the owner = %+v", a)
	}
	if envs, _ := s.Environments(ctx); len(envs) != 2 || envs[0].TeamID != "" || len(envs[1].Repos) != 1 && len(envs[0].Repos) != 1 {
		t.Errorf("projects = %+v", envs)
	}
	var account string
	s.r.QueryRow(`SELECT account_id FROM github_accounts WHERE id=7`).Scan(&account)
	if account != "own" {
		t.Errorf("the GitHub connection belongs to %q, want the owner", account)
	}
	if x, _ := s.Session(ctx, "s1"); x.Visibility != VisibilityWorkspace || x.TeamID != "" || x.AccountID != "" {
		t.Errorf("session = %+v", x)
	}
	if err := s.CreateEnvironment(ctx, &Environment{ID: "e3", Name: "WEB"}); !errors.Is(err, ErrConflict) {
		t.Errorf("names stay unique in the workspace: %v", err)
	}
}

func TestSessionQuery(t *testing.T) {
	s := open(t)
	for _, title := range []string{"Fix the Sidebar", "Redesign the sidebar", "100% coverage", "Add a_b", "Unrelated"} {
		if err := s.CreateSession(ctx, &Session{ID: NewID(), Title: title, Branch: "b", Source: "test", Status: "idle", Sandbox: "none"}); err != nil {
			t.Fatal(err)
		}
	}
	count := func(q string) int {
		list, err := s.Sessions(ctx, SessionFilter{Query: q})
		if err != nil {
			t.Fatal(err)
		}
		return len(list)
	}
	for q, want := range map[string]int{"sidebar": 2, "SIDEBAR": 2, "%": 1, "_": 1, "a_b": 1, "nothing": 0, "": 5} {
		if got := count(q); got != want {
			t.Errorf("query %q matched %d, want %d", q, got, want)
		}
	}
}
