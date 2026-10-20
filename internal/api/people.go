package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/brdsmth/beethoven/internal/access"
	"github.com/brdsmth/beethoven/internal/store"
)

// People: owners and admins invite people and set their roles; everyone
// manages their own profile and API tokens.

// forbid refuses a request the actor may not make.
func forbid(w http.ResponseWriter, what string) {
	fail(w, http.StatusForbidden, "you can't "+what)
}

// hidden answers like a missing resource, for one the actor may not see.
func hidden(w http.ResponseWriter) { fail(w, http.StatusNotFound, "not found") }

// audit records a change to who may do what. A failure to record is logged,
// not returned: the change has been made.
func (s *Server) audit(r *http.Request, team, action, target string, detail any) {
	who := actor(r)
	e := store.AuditEvent{ActorID: who.AccountID, Actor: who.Name, TeamID: team, Action: action, Target: target}
	if who.Server {
		e.ActorID = ""
	}
	if err := s.M.Store().Audit(r.Context(), e, detail); err != nil {
		s.Log.Warn("audit", "action", action, "err", err)
	}
}

// personView is someone as others see them: emails only for admins.
type personView struct {
	store.Account
	GitHub string `json:"github,omitempty"` // their GitHub login, if they connected one
}

func (s *Server) listPeople(w http.ResponseWriter, r *http.Request) {
	who := actor(r)
	list, err := s.M.Store().Accounts(r.Context())
	if err != nil {
		replyErr(w, err)
		return
	}
	logins := map[string]string{}
	if app, _ := s.GitHub.App(r.Context()); app != nil {
		logins, _ = s.M.Store().GitHubConnections(r.Context(), app.ID)
	}
	out := make([]personView, 0, len(list))
	for _, a := range list {
		if !who.Admin() && a.ID != who.AccountID {
			a.Email = ""
		}
		out = append(out, personView{Account: a, GitHub: logins[a.ID]})
	}
	reply(w, http.StatusOK, out)
}

// updatePerson changes someone's role or deactivates them. Only owners
// make, unmake or deactivate owners.
func (s *Server) updatePerson(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role     string `json:"role"`
		Disabled bool   `json:"disabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	who := actor(r)
	if !who.Admin() {
		forbid(w, "change people's roles")
		return
	}
	before, err := s.M.Store().Account(r.Context(), r.PathValue("id"))
	if err != nil {
		replyErr(w, err)
		return
	}
	if (before.Role == store.RoleOwner || in.Role == store.RoleOwner) && !who.Owner() {
		forbid(w, "change an owner, or make one: only owners can")
		return
	}
	if before.ID == who.AccountID && in.Disabled {
		fail(w, http.StatusBadRequest, "you can't deactivate yourself")
		return
	}
	after, err := s.M.Store().UpdateAccount(r.Context(), before.ID, in.Role, in.Disabled)
	if err != nil {
		replyErr(w, err)
		return
	}
	s.audit(r, "", "account.updated", after.ID, map[string]any{
		"email": after.Email, "role": [2]string{before.Role, after.Role}, "disabled": [2]bool{before.Disabled, after.Disabled},
	})
	reply(w, http.StatusOK, after)
}

// ------------------------------------------------------------------ invites

// inviteView is an invitation with whatever helps someone tell it apart.
type inviteView struct {
	store.Invite
	Team string `json:"team,omitempty"` // the team's name
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request) {
	if !actor(r).Admin() {
		forbid(w, "see invitations")
		return
	}
	list, err := s.M.Store().Invites(r.Context())
	if err != nil {
		replyErr(w, err)
		return
	}
	out := make([]inviteView, 0, len(list))
	for _, in := range list {
		v := inviteView{Invite: in}
		if t, err := s.M.Store().Team(r.Context(), in.TeamID); err == nil {
			v.Team = t.Name
		}
		out = append(out, v)
	}
	reply(w, http.StatusOK, out)
}

// createInvite makes a single-use link that creates an account. The link
// (its token) is in the reply only.
func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role          string `json:"role"`
		Email         string `json:"email"`
		TeamID        string `json:"team_id"`
		Note          string `json:"note"`
		FirstPrompt   string `json:"first_prompt"`
		EnvironmentID string `json:"environment_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	who := actor(r)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Note, in.FirstPrompt = strings.TrimSpace(in.Note), strings.TrimSpace(in.FirstPrompt)
	switch {
	case !who.Admin():
		forbid(w, "invite people")
		return
	case in.Role == store.RoleOwner && !who.Owner():
		forbid(w, "invite an owner: only owners can")
		return
	case in.Email != "" && (!strings.Contains(in.Email, "@") || len(in.Email) > 254):
		fail(w, http.StatusBadRequest, "enter an email address, or none")
		return
	case len(in.Note) > maxInviteNote:
		fail(w, http.StatusBadRequest, fmt.Sprintf("use a note of at most %d characters", maxInviteNote))
		return
	case len(in.FirstPrompt) > maxFirstPrompt:
		fail(w, http.StatusBadRequest, fmt.Sprintf("use a first session of at most %d characters", maxFirstPrompt))
		return
	}
	if in.TeamID != "" {
		if _, err := s.M.Store().Team(r.Context(), in.TeamID); err != nil {
			fail(w, http.StatusBadRequest, "no such team")
			return
		}
	}
	if in.EnvironmentID != "" {
		if e, err := s.M.Store().Environment(r.Context(), in.EnvironmentID); err != nil || e.TeamID != in.TeamID {
			fail(w, http.StatusBadRequest, "choose one of the team's projects")
			return
		}
	}
	if in.Email != "" && in.TeamID == "" { // someone who's here already can still be invited to a team
		if _, _, err := s.M.Store().AccountByEmail(r.Context(), in.Email); err == nil {
			fail(w, http.StatusConflict, in.Email+" already has an account")
			return
		}
	}
	creator := who.AccountID
	if creator == "" {
		fail(w, http.StatusBadRequest, "create an account first: invitations come from someone")
		return
	}
	inv := store.Invite{Role: in.Role, Email: in.Email, TeamID: in.TeamID, Note: in.Note, FirstPrompt: in.FirstPrompt,
		EnvironmentID: in.EnvironmentID, CreatedBy: creator}
	token, err := s.M.Store().CreateInvite(r.Context(), &inv)
	if err != nil {
		replyErr(w, err)
		return
	}
	s.audit(r, in.TeamID, "invite.created", inv.ID, map[string]string{"role": inv.Role, "email": inv.Email})
	reply(w, http.StatusCreated, map[string]any{"invite": inv, "token": token})
}

func (s *Server) deleteInvite(w http.ResponseWriter, r *http.Request) {
	if !actor(r).Admin() {
		forbid(w, "withdraw invitations")
		return
	}
	if err := s.M.Store().DeleteInvite(r.Context(), r.PathValue("id")); err != nil {
		replyErr(w, err)
		return
	}
	s.audit(r, "", "invite.withdrawn", r.PathValue("id"), nil)
	w.WriteHeader(http.StatusNoContent)
}

const (
	maxInviteNote  = 280
	maxFirstPrompt = 4000
)

// inviteTeam is what an invitation's page shows of the team it adds to.
type inviteTeam struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Color       string   `json:"color"`
	Members     int      `json:"members"`
	Projects    []string `json:"projects"` // the names of its projects
}

// inviteInfo describes an invitation to whoever holds its link: in full
// while it's open, otherwise what became of it ("used" or "expired") and
// who sent it, so they know whom to ask for another.
func (s *Server) inviteInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	in, err := s.M.Store().AnyInvite(ctx, r.URL.Query().Get("token"))
	if err != nil {
		fail(w, http.StatusNotFound, "this invitation has expired, was used, or was withdrawn")
		return
	}
	out := map[string]any{"state": "open", "created_at": in.CreatedAt, "expires_at": in.ExpiresAt}
	if by, err := s.M.Store().Account(ctx, in.CreatedBy); err == nil {
		out["from"] = firstOf(by.Name, by.Email)
	}
	if !in.Open() {
		out["state"] = map[bool]string{true: "used", false: "expired"}[in.UsedAt != nil]
		reply(w, http.StatusOK, out)
		return
	}
	out["role"], out["email"], out["note"] = in.Role, in.Email, in.Note
	if t, err := s.M.Store().Team(ctx, in.TeamID); err == nil && t.ArchivedAt == nil {
		it := inviteTeam{Name: t.Name, Description: t.Description, Color: t.Color, Members: t.Members, Projects: []string{}}
		envs, _ := s.M.Store().Environments(ctx)
		for _, e := range envs {
			if e.TeamID == t.ID {
				it.Projects = append(it.Projects, e.Name)
			}
		}
		out["team"] = it
	}
	app, _ := s.GitHub.App(ctx)
	out["github"] = app != nil
	if a, err := s.account(r); err == nil { // signed in already: the invitation can add them to its team
		out["signed_in"] = map[string]string{"name": a.Name, "email": a.Email}
	}
	reply(w, http.StatusOK, out)
}

// joinInvite uses an invitation for whoever is signed in: it adds them to
// its team.
func (s *Server) joinInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	a, err := s.account(r)
	if err != nil {
		fail(w, http.StatusUnauthorized, "sign in first")
		return
	}
	team, err := s.M.Store().JoinInvite(r.Context(), in.Token, a)
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "this invitation has expired, was used, or was withdrawn")
		return
	case err != nil:
		replyErr(w, err)
		return
	}
	s.Log.Info("joined a team through an invitation", "email", a.Email, "team", team)
	e := store.AuditEvent{ActorID: a.ID, Actor: firstOf(a.Name, a.Email), TeamID: team, Action: "team.member_joined", Target: a.ID}
	s.M.Store().Audit(r.Context(), e, map[string]string{"via": "invitation"})
	s.authState(w, r)
}

// acceptInvite creates an account from an invitation, and signs in.
func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		credentials
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.clean()
	if msg := checkCredentials(in.credentials); msg != "" {
		fail(w, http.StatusBadRequest, msg)
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		replyErr(w, err)
		return
	}
	a, err := s.M.Store().AcceptInvite(r.Context(), in.Token, store.Account{Email: in.Email, Name: in.Name}, hash)
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "this invitation has expired, was used, or was withdrawn")
		return
	case errors.Is(err, store.ErrConflict):
		fail(w, http.StatusConflict, in.Email+" already has an account: sign in instead")
		return
	case err != nil:
		replyErr(w, err)
		return
	}
	s.Log.Info("accepted an invitation", "email", a.Email, "role", a.Role)
	e := store.AuditEvent{ActorID: a.ID, Actor: firstOf(a.Name, a.Email), Action: "account.created", Target: a.ID}
	s.M.Store().Audit(r.Context(), e, map[string]string{"email": a.Email, "role": a.Role})
	s.signInAs(w, r, a, http.StatusCreated)
}

// checkCredentials explains what's wrong with new credentials, if anything.
func checkCredentials(in credentials) string {
	switch {
	case !strings.Contains(in.Email, "@") || len(in.Email) > 254:
		return "enter an email address"
	case len(in.Password) < minPassword:
		return fmt.Sprintf("use a password of at least %d characters", minPassword)
	case len(in.Password) > 1024:
		return "use a password of at most 1024 characters"
	case len(in.Name) > 100:
		return "use a name of at most 100 characters"
	}
	return ""
}

// ----------------------------------------------------------------------- me

// updateMe changes the viewer's own name and default team.
func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name          *string `json:"name"`
		DefaultTeamID *string `json:"default_team_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	who := actor(r)
	if !who.IsPerson() {
		fail(w, http.StatusBadRequest, "sign in to change your profile")
		return
	}
	if in.Name != nil {
		if len(*in.Name) > 100 {
			fail(w, http.StatusBadRequest, "use a name of at most 100 characters")
			return
		}
		if err := s.M.Store().UpdateProfile(r.Context(), who.AccountID, strings.TrimSpace(*in.Name)); err != nil {
			replyErr(w, err)
			return
		}
	}
	if in.DefaultTeamID != nil {
		if *in.DefaultTeamID != "" && !who.Member(*in.DefaultTeamID) {
			fail(w, http.StatusBadRequest, "join that team first")
			return
		}
		if err := s.M.Store().SetDefaultTeam(r.Context(), who.AccountID, *in.DefaultTeamID); err != nil {
			replyErr(w, err)
			return
		}
	}
	s.authState(w, r)
}

// updateOnboarding records progress in getting started: the tour was seen,
// a session was started, or they're done with it.
func (s *Server) updateOnboarding(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TourSeen bool `json:"tour_seen"`
		Started  bool `json:"started"`
		Done     bool `json:"done"`
	}
	if !decode(w, r, &in) {
		return
	}
	who := actor(r)
	if !who.IsPerson() {
		fail(w, http.StatusBadRequest, "sign in first")
		return
	}
	o, err := s.M.Store().Onboarding(r.Context(), who.AccountID)
	if err != nil {
		replyErr(w, err)
		return
	}
	if o != nil {
		if in.TourSeen {
			o.Tour = false
		}
		if in.Started {
			o.Started = true
		}
		if in.Done {
			o = nil
		}
		if err := s.M.Store().SetOnboarding(r.Context(), who.AccountID, o); err != nil {
			replyErr(w, err)
			return
		}
	}
	s.authState(w, r)
}

// --------------------------------------------------------------- API tokens

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	who := actor(r)
	if !who.IsPerson() {
		reply(w, http.StatusOK, []any{})
		return
	}
	list, err := s.M.Store().APITokens(r.Context(), who.AccountID)
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, orEmpty(list))
}

// createToken makes a personal API token; its value is in the reply only.
func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	who := actor(r)
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case !who.IsPerson():
		fail(w, http.StatusBadRequest, "sign in to make a personal token")
		return
	case in.Name == "" || len(in.Name) > 60:
		fail(w, http.StatusBadRequest, "name the token, in at most 60 characters")
		return
	}
	t, value, err := s.M.Store().CreateAPIToken(r.Context(), who.AccountID, in.Name)
	if err != nil {
		replyErr(w, err)
		return
	}
	s.audit(r, "", "token.created", t.ID, map[string]string{"name": t.Name})
	reply(w, http.StatusCreated, map[string]any{"token": t, "value": value})
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	who := actor(r)
	if err := s.M.Store().DeleteAPIToken(r.Context(), who.AccountID, r.PathValue("id")); err != nil {
		replyErr(w, err)
		return
	}
	s.audit(r, "", "token.revoked", r.PathValue("id"), nil)
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------------- audit

func (s *Server) auditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	who, team := actor(r), q.Get("team")
	if !who.Admin() && (team == "" || !access.ManageTeam(who, team)) {
		forbid(w, "see the audit log")
		return
	}
	var before int64
	fmt.Sscan(q.Get("before"), &before)
	list, err := s.M.Store().AuditEvents(r.Context(), team, before, 100)
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, orEmpty(list))
}
