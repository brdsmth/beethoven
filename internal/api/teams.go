package api

import (
	"errors"
	"net/http"

	"github.com/brdsmth/beethoven/internal/access"
	"github.com/brdsmth/beethoven/internal/store"
)

// Teams: admins create them; leads (and admins) run them; anyone may join
// an open one, and leave any.

// teamView is a team as the viewer sees it.
type teamView struct {
	store.Team
	Role    string `json:"role,omitempty"` // the viewer's: lead, member, or "" when not in it
	Manages bool   `json:"manages"`        // the viewer may edit it
}

func view(who access.Actor, t store.Team) teamView {
	return teamView{Team: t, Role: who.Teams[t.ID], Manages: access.ManageTeam(who, t.ID)}
}

// listTeams lists the teams the viewer may see: anyone may see which
// teams exist, as they may join open ones. Archived teams show to their
// members and admins; a personal team only to its person.
func (s *Server) listTeams(w http.ResponseWriter, r *http.Request) {
	who := actor(r)
	list, err := s.M.Store().Teams(r.Context())
	if err != nil {
		replyErr(w, err)
		return
	}
	out := make([]teamView, 0, len(list))
	for _, t := range list {
		if !access.SeeTeam(who, t) {
			continue
		}
		out = append(out, view(who, t))
	}
	reply(w, http.StatusOK, out)
}

// team loads the team a request names, by id or slug, or replies.
func (s *Server) team(w http.ResponseWriter, r *http.Request) (store.Team, bool) {
	t, err := s.M.Store().TeamBySlug(r.Context(), r.PathValue("id"))
	if err != nil {
		replyErr(w, err)
		return t, false
	}
	if !access.SeeTeam(actor(r), t) {
		hidden(w)
		return t, false
	}
	return t, true
}

func (s *Server) getTeam(w http.ResponseWriter, r *http.Request) {
	t, ok := s.team(w, r)
	if !ok {
		return
	}
	members, err := s.M.Store().TeamMembers(r.Context(), t.ID)
	if err != nil {
		replyErr(w, err)
		return
	}
	who := actor(r)
	for i := range members {
		if !who.Admin() && members[i].ID != who.AccountID {
			members[i].Email = ""
		}
	}
	reply(w, http.StatusOK, map[string]any{"team": view(who, t), "members": orEmpty(members)})
}

type teamReq struct {
	Name                 string `json:"name"`
	Slug                 string `json:"slug"`
	Description          string `json:"description"`
	Color                string `json:"color"`
	JoinPolicy           string `json:"join_policy"`
	DefaultVisibility    string `json:"default_visibility"`
	DefaultEnvironmentID string `json:"default_environment_id"`
}

func (in teamReq) apply(t *store.Team) {
	t.Name, t.Slug, t.Description, t.Color = in.Name, in.Slug, in.Description, in.Color
	t.JoinPolicy, t.DefaultVisibility, t.DefaultEnvironmentID = in.JoinPolicy, in.DefaultVisibility, in.DefaultEnvironmentID
}

// checkDefaultProject refuses a default project the team doesn't own.
func (s *Server) checkDefaultProject(r *http.Request, t store.Team) error {
	if t.DefaultEnvironmentID == "" {
		return nil
	}
	e, err := s.M.Store().Environment(r.Context(), t.DefaultEnvironmentID)
	if err != nil || (e.TeamID != "" && e.TeamID != t.ID) {
		return errors.New("the default project must be the team's or the workspace's")
	}
	return nil
}

// createTeam makes a team; its creator leads it.
func (s *Server) createTeam(w http.ResponseWriter, r *http.Request) {
	var in teamReq
	if !decode(w, r, &in) {
		return
	}
	who := actor(r)
	if !who.Admin() {
		forbid(w, "create teams: ask an admin")
		return
	}
	var t store.Team
	in.apply(&t)
	if err := s.checkDefaultProject(r, t); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	lead := ""
	if who.IsPerson() {
		lead = who.AccountID
	}
	if err := s.M.Store().CreateTeam(r.Context(), &t, lead); err != nil {
		replyErr(w, err)
		return
	}
	s.audit(r, t.ID, "team.created", t.ID, map[string]string{"name": t.Name, "slug": t.Slug})
	if lead != "" {
		who.Teams[t.ID] = store.TeamLead
	}
	reply(w, http.StatusCreated, view(who, t))
}

func (s *Server) updateTeam(w http.ResponseWriter, r *http.Request) {
	var in teamReq
	if !decode(w, r, &in) {
		return
	}
	t, ok := s.team(w, r)
	if !ok {
		return
	}
	who := actor(r)
	if !access.ManageTeam(who, t.ID) {
		forbid(w, "change this team: ask one of its leads")
		return
	}
	before := t
	in.apply(&t)
	if t.Personal {
		t.Slug, t.JoinPolicy = t.ID, store.JoinInvite // its short name and that nobody joins don't change
	}
	if err := s.checkDefaultProject(r, t); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.M.Store().SaveTeam(r.Context(), &t); err != nil {
		replyErr(w, err)
		return
	}
	s.audit(r, t.ID, "team.updated", t.ID, map[string]any{"before": before, "after": t})
	reply(w, http.StatusOK, view(who, t))
}

func (s *Server) archiveTeam(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Archived bool `json:"archived"`
	}
	if !decode(w, r, &in) {
		return
	}
	t, ok := s.team(w, r)
	if !ok {
		return
	}
	if !access.ManageTeam(actor(r), t.ID) {
		forbid(w, "archive this team")
		return
	}
	if t.Personal {
		fail(w, http.StatusBadRequest, "a personal team can't be archived")
		return
	}
	if err := s.M.Store().ArchiveTeam(r.Context(), t.ID, in.Archived); err != nil {
		replyErr(w, err)
		return
	}
	action := "team.archived"
	if !in.Archived {
		action = "team.restored"
	}
	s.audit(r, t.ID, action, t.ID, nil)
	w.WriteHeader(http.StatusNoContent)
}

// setMember adds someone to a team or changes their role there.
func (s *Server) setMember(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role string `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	t, ok := s.team(w, r)
	if !ok {
		return
	}
	if !access.ManageTeam(actor(r), t.ID) {
		forbid(w, "change this team's members: ask one of its leads")
		return
	}
	if t.ArchivedAt != nil {
		fail(w, http.StatusConflict, t.Name+" is archived")
		return
	}
	id := r.PathValue("account")
	a, err := s.M.Store().Account(r.Context(), id)
	if err != nil || a.Disabled {
		fail(w, http.StatusBadRequest, "no such person")
		return
	}
	if in.Role == "" {
		in.Role = store.TeamMemberRole
	}
	before, _ := s.M.Store().Memberships(r.Context(), id)
	if err := s.M.Store().SetTeamMember(r.Context(), t.ID, id, in.Role); err != nil {
		replyErr(w, err)
		return
	}
	action := "team.member_added"
	if before[t.ID] != "" {
		action = "team.member_role_changed"
	}
	s.audit(r, t.ID, action, id, map[string]string{"person": firstOf(a.Name, a.Email), "role": in.Role, "was": before[t.ID]})
	w.WriteHeader(http.StatusNoContent)
}

// removeMember takes someone out of a team: a lead or admin can, and
// anyone can leave.
func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) {
	t, ok := s.team(w, r)
	if !ok {
		return
	}
	who, id := actor(r), r.PathValue("account")
	if id != who.AccountID && !access.ManageTeam(who, t.ID) {
		forbid(w, "remove people from this team")
		return
	}
	if err := s.M.Store().RemoveTeamMember(r.Context(), t.ID, id); err != nil {
		replyErr(w, err)
		return
	}
	action := "team.member_removed"
	if id == who.AccountID {
		action = "team.member_left"
	}
	s.audit(r, t.ID, action, id, nil)
	w.WriteHeader(http.StatusNoContent)
}

// joinTeam adds the viewer to an open team.
func (s *Server) joinTeam(w http.ResponseWriter, r *http.Request) {
	t, ok := s.team(w, r)
	if !ok {
		return
	}
	who := actor(r)
	if !access.JoinTeam(who, t) {
		forbid(w, "join this team: ask one of its leads to add you")
		return
	}
	if who.Member(t.ID) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.M.Store().SetTeamMember(r.Context(), t.ID, who.AccountID, store.TeamMemberRole); err != nil {
		replyErr(w, err)
		return
	}
	s.audit(r, t.ID, "team.member_joined", who.AccountID, nil)
	w.WriteHeader(http.StatusNoContent)
}
