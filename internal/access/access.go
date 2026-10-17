// Package access decides who may do what. Every rule lives here, as pure
// functions over an Actor and the stored rows, and the store mirrors the
// session rules as one SQL predicate for lists (store.Session visibility):
// handlers ask, they don't decide.
//
// The server is one workspace of people. Teams group people and own
// resources (sessions, projects, automations, secrets); a resource without
// a team belongs to the workspace, as everything did before teams.
// Workspace owners and admins manage everything; team leads manage their
// team; members use what their teams own and what the workspace owns.
package access

import (
	"context"

	"github.com/brdsmth/beethoven/internal/store"
)

// Actor is who a request or a piece of work acts for.
type Actor struct {
	AccountID string            // the person; for the server, its first owner ("" if none)
	Server    bool              // the server itself, not a person: it acts for its owner
	Name      string            // for logs and audit
	Role      string            // workspace role; the server acts as an owner
	Teams     map[string]string // team id → team role (lead or member)
}

// Server is the actor for requests no person makes: on a server nobody
// signs in to, with the server's own token, and for background work.
func Server(ownerID string) Actor {
	return Actor{AccountID: ownerID, Server: true, Name: "server", Role: store.RoleOwner}
}

// Person is the actor for a signed-in account and its team memberships.
func Person(a store.Account, teams map[string]string) Actor {
	if teams == nil {
		teams = map[string]string{}
	}
	return Actor{AccountID: a.ID, Name: firstOf(a.Name, a.Email), Role: a.Role, Teams: teams}
}

// Admin reports whether the actor manages the workspace.
func (a Actor) Admin() bool { return a.Role == store.RoleOwner || a.Role == store.RoleAdmin }

// Owner reports whether the actor is a workspace owner.
func (a Actor) Owner() bool { return a.Role == store.RoleOwner }

// Member reports whether the actor belongs to a team.
func (a Actor) Member(teamID string) bool {
	_, ok := a.Teams[teamID]
	return ok
}

// Lead reports whether the actor leads a team.
func (a Actor) Lead(teamID string) bool { return a.Teams[teamID] == store.TeamLead }

// TeamIDs lists the actor's teams.
func (a Actor) TeamIDs() []string {
	ids := make([]string, 0, len(a.Teams))
	for id := range a.Teams {
		ids = append(ids, id)
	}
	return ids
}

// IsPerson reports whether the actor is a person rather than the server.
func (a Actor) IsPerson() bool { return !a.Server && a.AccountID != "" }

// Viewer is the store's view of the actor, for list predicates.
func (a Actor) Viewer() store.Viewer {
	return store.Viewer{AccountID: a.AccountID, Admin: a.Admin(), Owner: a.Owner(), Teams: a.TeamIDs()}
}

// ----------------------------------------------------------------- teams

// UseTeam: start work owned by a team, and read what it owns.
func UseTeam(a Actor, teamID string) bool { return teamID == "" || a.Admin() || a.Member(teamID) }

// ManageTeam: edit a team, its members, secrets and channels.
func ManageTeam(a Actor, teamID string) bool { return a.Admin() || a.Lead(teamID) }

// JoinTeam: join a team by oneself.
func JoinTeam(a Actor, t store.Team) bool {
	return a.AccountID != "" && t.ArchivedAt == nil && (t.JoinPolicy == store.JoinOpen || a.Admin())
}

// --------------------------------------------------------------- sessions

// ReadSession: see a session, its timeline and its files.
func ReadSession(a Actor, s store.Session) bool {
	switch s.Visibility {
	case store.VisibilityPrivate:
		return (a.AccountID != "" && a.AccountID == s.AccountID) || a.Owner()
	case store.VisibilityTeam:
		return a.Admin() || a.Member(s.TeamID)
	}
	return true
}

// PromptSession: send prompts, stop work, open pull requests. Beyond
// reading, a team's session needs membership in the team; a private one,
// being its owner.
func PromptSession(a Actor, s store.Session) bool {
	if !ReadSession(a, s) {
		return false
	}
	if s.Visibility == store.VisibilityPrivate {
		return a.AccountID != "" && a.AccountID == s.AccountID
	}
	return UseTeam(a, s.TeamID)
}

// ManageSession: archive a session or change who sees it: its owner, a
// lead of its team, or an admin.
func ManageSession(a Actor, s store.Session) bool {
	if !ReadSession(a, s) {
		return false
	}
	if a.AccountID != "" && a.AccountID == s.AccountID {
		return true
	}
	if s.Visibility == store.VisibilityPrivate {
		return a.Owner()
	}
	return a.Admin() || (s.TeamID != "" && a.Lead(s.TeamID))
}

// --------------------------------------------- projects, automations, tools

// UseEnvironment: see a project (environment) and start sessions in it.
func UseEnvironment(a Actor, e store.Environment) bool { return UseTeam(a, e.TeamID) }

// ManageEnvironment: edit or delete a project, and its secrets. A team's
// members manage its projects; anyone manages the workspace's.
func ManageEnvironment(a Actor, e store.Environment) bool { return UseTeam(a, e.TeamID) }

// UseAutomation: see an automation and its runs.
func UseAutomation(a Actor, x store.Automation) bool { return UseTeam(a, x.TeamID) }

// ManageAutomation: edit, delete or run an automation.
func ManageAutomation(a Actor, x store.Automation) bool { return UseTeam(a, x.TeamID) }

// Scopes for secrets and MCP servers: "" (global), owner/name (a
// repository), env:<id> (a project), team:<id> (a team).

// ReadScope: see the names in a scope.
func ReadScope(a Actor, scope string, env *store.Environment) bool {
	switch {
	case env != nil:
		return UseEnvironment(a, *env)
	case store.IsTeamScope(scope):
		return UseTeam(a, store.TeamOfScope(scope))
	}
	return true
}

// WriteScope: set or delete in a scope. Global and repository scopes belong
// to the workspace's admins; a team's to its leads; a project's to whoever
// manages it.
func WriteScope(a Actor, scope string, env *store.Environment) bool {
	switch {
	case env != nil:
		return ManageEnvironment(a, *env)
	case store.IsTeamScope(scope):
		return ManageTeam(a, store.TeamOfScope(scope))
	}
	return a.Admin()
}

// ------------------------------------------------------------------ context

type actorKey struct{}

// With carries an actor in ctx: the person work is done for, whose GitHub
// it uses.
func With(ctx context.Context, a Actor) context.Context { return context.WithValue(ctx, actorKey{}, a) }

// From returns the actor ctx carries, if any.
func From(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(Actor)
	return a, ok
}

// For carries just a person, by account id, for background work done on
// their behalf (a session's prompts).
func For(ctx context.Context, accountID string) context.Context {
	return With(ctx, Actor{AccountID: accountID, Role: store.RoleMember})
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
