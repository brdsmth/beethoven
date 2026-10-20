package api_test

import (
	"testing"
)

// An invitation tells its person about the team and starts their getting
// started; someone who's already here can use one to join a team.
func TestInvitationOnboarding(t *testing.T) {
	_, srv, who := people(t, map[string]string{"max": "member"})
	owner, max := who["owner"], who["max"]
	if code, st := max.call("GET", "/api/auth", ""); code != 200 || field(st, "onboarding", "tour") != true {
		t.Errorf("an invited person starts getting started: %v", st)
	}
	if _, st := owner.call("GET", "/api/auth", ""); field(st, "onboarding") != nil {
		t.Errorf("the owner gets started too: %v", st)
	}
	_, out := owner.call("POST", "/api/teams", `{"name":"Mobile","color":"#7c3aed"}`)
	mobile := field(out, "id").(string)

	// An empty team, a note and a first session.
	code, out := owner.call("POST", "/api/invites", `{"role":"member","email":"sam@x.dev","team_id":"`+mobile+`",
		"note":"Welcome!","first_prompt":"Explain the code"}`)
	if code != 201 {
		t.Fatalf("invite: %d %v", code, out)
	}
	link := field(out, "token").(string)
	sam := newBrowser(t, srv.URL)
	code, info := sam.call("GET", "/api/auth/invite?token="+link, "")
	if code != 200 || field(info, "state") != "open" || field(info, "note") != "Welcome!" || field(info, "team", "name") != "Mobile" ||
		len(field(info, "team", "projects").([]any)) != 0 || field(info, "signed_in") != nil {
		t.Errorf("invite info: %d %v", code, info)
	}
	if code, out := sam.call("POST", "/api/auth/invite", `{"token":"`+link+`","name":"Sam","email":"sam@x.dev","password":"correct horse"}`); code != 201 {
		t.Fatalf("accept: %d %v", code, out)
	}
	_, st := sam.call("GET", "/api/auth", "")
	if field(st, "onboarding", "team_id") != mobile || field(st, "onboarding", "prompt") != "Explain the code" ||
		field(st, "onboarding", "invited_by_name") != "Olive" || field(st, "default_team_id") != mobile {
		t.Errorf("sam's getting started: %v", st)
	}
	if _, info := sam.call("GET", "/api/auth/invite?token="+link, ""); field(info, "state") != "used" || field(info, "from") != "Olive" ||
		field(info, "note") != nil {
		t.Errorf("a used invitation: %v", info)
	}

	// The team's first project becomes its default.
	code, out = sam.call("POST", "/api/environments", `{"team_id":"`+mobile+`","name":"mobile","repos":[]}`)
	if code != 201 {
		t.Fatalf("sam gives Mobile a project: %d %v", code, out)
	}
	project := field(out, "id").(string)
	if _, team := sam.call("GET", "/api/teams/"+mobile, ""); field(team, "team", "default_environment_id") != project {
		t.Errorf("Mobile's default project: %v", team)
	}
	if code, _ := owner.call("POST", "/api/invites", `{"role":"member","team_id":"`+mobile+`","environment_id":"nope"}`); code != 400 {
		t.Errorf("an invitation to someone else's project: %d", code)
	}
	// Deleting it leaves no default, so the next one becomes it.
	if code, _ := sam.call("DELETE", "/api/environments/"+project, ""); code != 204 {
		t.Fatalf("delete the project: %d", code)
	}
	_, out = sam.call("POST", "/api/environments", `{"team_id":"`+mobile+`","name":"mobile","repos":[]}`)
	project = field(out, "id").(string)
	if _, team := sam.call("GET", "/api/teams/"+mobile, ""); field(team, "team", "default_environment_id") != project {
		t.Errorf("Mobile's default after the first was deleted: %v", team)
	}

	// Progress: the tour, a first session, done.
	if _, st := sam.call("PUT", "/api/me/onboarding", `{"tour_seen":true}`); field(st, "onboarding", "tour") != nil ||
		field(st, "onboarding", "team_id") != mobile {
		t.Errorf("after the tour: %v", st)
	}
	if code, _ := sam.call("POST", "/api/sessions", `{"prompt":"hello","team_id":"`+mobile+`"}`); code != 201 {
		t.Fatalf("sam's first session: %d", code)
	}
	if _, st := sam.call("GET", "/api/auth", ""); field(st, "onboarding", "started") != true {
		t.Errorf("the first session isn't counted: %v", st)
	}
	if _, st := sam.call("PUT", "/api/me/onboarding", `{"done":true}`); field(st, "onboarding") != nil {
		t.Errorf("done: %v", st)
	}

	// Max is here already, done with getting started: an invitation adds him to the team, his role as it was.
	max.call("PUT", "/api/me/onboarding", `{"done":true}`)
	if code, _ := owner.call("POST", "/api/invites", `{"role":"admin","email":"max@x.dev","team_id":"`+mobile+`"}`); code != 409 {
		t.Errorf("an invitation that would change max's role: %d", code)
	}
	code, out = owner.call("POST", "/api/invites", `{"role":"member","email":"max@x.dev","team_id":"`+mobile+`"}`)
	if code != 201 {
		t.Fatalf("invite max to Mobile: %d %v", code, out)
	}
	join := `{"token":"` + field(out, "token").(string) + `"}`
	if _, info := max.call("GET", "/api/auth/invite?token="+field(out, "token").(string), ""); field(info, "signed_in", "email") != "max@x.dev" {
		t.Errorf("signed in on the invitation: %v", info)
	}
	if code, _ := newBrowser(t, srv.URL).call("POST", "/api/auth/invite/join", join); code != 401 {
		t.Errorf("join, signed out: %d", code)
	}
	if code, _ := sam.call("POST", "/api/auth/invite/join", join); code != 400 {
		t.Errorf("sam uses max's invitation: %d", code)
	}
	code, st = max.call("POST", "/api/auth/invite/join", join)
	if code != 200 || field(st, "teams", mobile) != "member" || field(st, "account", "role") != "member" ||
		field(st, "onboarding", "team_id") != mobile || field(st, "onboarding", "started") != true || field(st, "onboarding", "tour") != nil {
		t.Errorf("max joins: %d %v", code, st)
	}
	if code, _ := max.call("POST", "/api/auth/invite/join", join); code != 404 {
		t.Errorf("an invitation used twice: %d", code)
	}
	_, out = owner.call("POST", "/api/invites", `{"role":"member"}`)
	if code, _ := max.call("POST", "/api/auth/invite/join", `{"token":"`+field(out, "token").(string)+`"}`); code != 400 {
		t.Errorf("join with an invitation to no team: %d", code)
	}
}
