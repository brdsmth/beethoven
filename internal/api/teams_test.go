package api_test

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brdsmth/beethoven/internal/api"
	"github.com/brdsmth/beethoven/internal/automation"
	"github.com/brdsmth/beethoven/internal/fake"
	"github.com/brdsmth/beethoven/internal/github"
	"github.com/brdsmth/beethoven/internal/store"
)

// call makes a request and decodes any JSON reply, object or list.
func (b *browser) call(method, path, body string, header ...string) (int, any) {
	b.t.Helper()
	req, _ := http.NewRequest(method, b.url+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func field(v any, path ...string) any {
	for _, k := range path {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}

func ids(v any) map[string]bool {
	out := map[string]bool{}
	list, _ := v.([]any)
	for _, x := range list {
		out[field(x, "id").(string)] = true
	}
	return out
}

// people starts a server that asks people to sign in, with an owner, and
// invites more: it returns a signed-in browser for each.
func people(t *testing.T, invite map[string]string) (*fake.Env, *httptest.Server, map[string]*browser) {
	env := fake.NewEnv(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &api.Server{M: env.M, Auto: automation.New(env.M, log), GitHub: github.New("test-token"), SignIn: true, Log: log}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	who := map[string]*browser{"owner": newBrowser(t, srv.URL)}
	if code, out, _ := who["owner"].do("POST", "/api/auth/setup", `{"name":"Olive","email":"owner@x.dev","password":"correct horse"}`); code != 201 {
		t.Fatalf("setup: %d %v", code, out)
	}
	for name, role := range invite {
		code, out := who["owner"].call("POST", "/api/invites", `{"role":"`+role+`"}`)
		if code != 201 {
			t.Fatalf("invite %s: %d %v", name, code, out)
		}
		b := newBrowser(t, srv.URL)
		body := `{"token":"` + field(out, "token").(string) + `","name":"` + name + `","email":"` + name + `@x.dev","password":"correct horse"}`
		if code, out := b.call("POST", "/api/auth/invite", body); code != 201 || field(out, "account", "role") != role {
			t.Fatalf("accept for %s: %d %v", name, code, out)
		}
		who[name] = b
	}
	return env, srv, who
}

func TestInvitesAndRoles(t *testing.T) {
	_, srv, who := people(t, map[string]string{"ada": "admin", "max": "member"})
	owner, ada, max := who["owner"], who["ada"], who["max"]

	if code, _ := max.call("POST", "/api/invites", `{"role":"member"}`); code != 403 {
		t.Errorf("a member invites: %d", code)
	}
	if code, _ := ada.call("POST", "/api/invites", `{"role":"owner"}`); code != 403 {
		t.Errorf("an admin invites an owner: %d", code)
	}
	code, out := ada.call("POST", "/api/invites", `{"role":"member","email":"zed@x.dev"}`)
	if code != 201 {
		t.Fatalf("admin invites: %d %v", code, out)
	}
	link := field(out, "token").(string)
	stranger := newBrowser(t, srv.URL)
	if code, info := stranger.call("GET", "/api/auth/invite?token="+link, ""); code != 200 || field(info, "from") != "ada" ||
		field(info, "email") != "zed@x.dev" {
		t.Errorf("invite info: %d %v", code, info)
	}
	if code, _ := stranger.call("POST", "/api/auth/invite", `{"token":"`+link+`","email":"eve@x.dev","password":"correct horse"}`); code != 400 {
		t.Errorf("someone else takes zed's invitation: %d", code)
	}
	if code, _ := stranger.call("GET", "/api/auth/invite?token=nope", ""); code != 404 {
		t.Errorf("a made-up invitation: %d", code)
	}

	// People and roles: emails only for admins; only owners touch owners.
	_, list := max.call("GET", "/api/people", "")
	for _, p := range list.([]any) {
		if field(p, "email") != "" && field(p, "name") != "max" {
			t.Errorf("a member sees %v's email", field(p, "name"))
		}
	}
	_, list = ada.call("GET", "/api/people", "")
	var maxID, ownerID string
	for _, p := range list.([]any) {
		switch field(p, "name") {
		case "max":
			maxID = field(p, "id").(string)
		case "Olive":
			ownerID = field(p, "id").(string)
		}
	}
	if code, _ := ada.call("PUT", "/api/people/"+ownerID, `{"role":"member"}`); code != 403 {
		t.Errorf("an admin demotes the owner: %d", code)
	}
	if code, out := owner.call("PUT", "/api/people/"+ownerID, `{"role":"admin"}`); code != 400 || !strings.Contains(field(out, "error").(string), "owner") {
		t.Errorf("the last owner demotes themselves: %d %v", code, out)
	}
	if code, _ := ada.call("PUT", "/api/people/"+maxID, `{"role":"member","disabled":true}`); code != 200 {
		t.Fatalf("an admin deactivates a member: %d", code)
	}
	if code, _ := max.call("GET", "/api/sessions", ""); code != 401 {
		t.Errorf("a deactivated member is still signed in: %d", code)
	}
	if code, _ := newBrowser(t, srv.URL).call("POST", "/api/auth/login", `{"email":"max@x.dev","password":"correct horse"}`); code != 401 {
		t.Errorf("a deactivated member signs in: %d", code)
	}

	// Personal tokens act as their person.
	code, out = ada.call("POST", "/api/tokens", `{"name":"laptop"}`)
	if code != 201 || !strings.HasPrefix(field(out, "value").(string), "bt_") {
		t.Fatalf("token: %d %v", code, out)
	}
	cli := newBrowser(t, srv.URL)
	if code, st := cli.call("GET", "/api/people", "", "Authorization", "Bearer "+field(out, "value").(string)); code != 200 || len(st.([]any)) != 3 {
		t.Errorf("with ada's token: %d %v", code, st)
	}
	if code, _ := cli.call("GET", "/api/people", "", "Authorization", "Bearer bt_made-up"); code != 401 {
		t.Errorf("with a made-up token: %d", code)
	}
	if code, audit := owner.call("GET", "/api/audit", ""); code != 200 || len(audit.([]any)) < 4 {
		t.Errorf("audit: %d %v", code, audit)
	}
	if code, _ := ada.call("GET", "/api/settings", ""); code != 200 {
		t.Errorf("settings for an admin: %d", code)
	}
}

func TestTeams(t *testing.T) {
	env, srv, who := people(t, map[string]string{"ann": "member", "bob": "member", "cy": "member", "ada": "admin"})
	owner, ann, bob, cy, ada := who["owner"], who["ann"], who["bob"], who["cy"], who["ada"]
	idOf := map[string]string{}
	_, list := owner.call("GET", "/api/people", "")
	for _, p := range list.([]any) {
		idOf[field(p, "name").(string)] = field(p, "id").(string)
	}

	if code, _ := ann.call("POST", "/api/teams", `{"name":"Web"}`); code != 403 {
		t.Errorf("a member creates a team: %d", code)
	}
	code, out := owner.call("POST", "/api/teams", `{"name":"Web","color":"#3366ff"}`)
	if code != 201 || field(out, "role") != "lead" || field(out, "slug") != "web" {
		t.Fatalf("team: %d %v", code, out)
	}
	web := field(out, "id").(string)
	for _, add := range []string{`/api/teams/web/members/` + idOf["ann"] + `|{"role":"lead"}`, `/api/teams/web/members/` + idOf["bob"] + `|{}`} {
		path, body, _ := strings.Cut(add, "|")
		if code, out := owner.call("PUT", path, body); code != 204 {
			t.Fatalf("add member %s: %d %v", path, code, out)
		}
	}
	if code, _ := bob.call("PUT", "/api/teams/web/members/"+idOf["cy"], `{}`); code != 403 {
		t.Errorf("a member adds people: %d", code)
	}
	if code, _ := cy.call("POST", "/api/teams/web/join", ""); code != 403 {
		t.Errorf("cy joins an invite-only team: %d", code)
	}

	// A team's project: only its people see and use it.
	code, out = bob.call("POST", "/api/environments", `{"team_id":"`+web+`","name":"App","repos":[]}`)
	if code != 201 {
		t.Fatalf("bob makes a team project: %d %v", code, out)
	}
	project := field(out, "id").(string)
	if code, _ := cy.call("POST", "/api/environments", `{"team_id":"`+web+`","name":"Mine"}`); code != 403 {
		t.Errorf("cy makes a project for a team he's not in: %d", code)
	}
	if _, envs := cy.call("GET", "/api/environments", ""); ids(envs)[project] {
		t.Error("cy sees the team's project")
	}
	if code, _ := cy.call("GET", "/api/environments/"+project, ""); code != 404 {
		t.Errorf("cy reads the team's project: %d", code)
	}

	// A team's sessions: team-visible by default.
	code, out = bob.call("POST", "/api/sessions", `{"prompt":"team work","environment_id":"`+project+`"}`)
	if code != 201 {
		t.Fatalf("bob starts a session: %d %v", code, out)
	}
	teamSession := field(out, "session", "id").(string)
	if field(out, "session", "team_id") != web || field(out, "session", "visibility") != "team" {
		t.Errorf("session = %v", field(out, "session"))
	}
	if code, _ := cy.call("POST", "/api/sessions", `{"prompt":"x","environment_id":"`+project+`"}`); code != 400 {
		t.Errorf("cy starts a session in the team's project: %d", code)
	}
	_, out = cy.call("POST", "/api/sessions", `{"prompt":"just mine","visibility":"private"}`)
	private := field(out, "session", "id").(string)
	_, out = cy.call("POST", "/api/sessions", `{"prompt":"for everyone"}`)
	open := field(out, "session", "id").(string)

	sees := func(b *browser) map[string]bool { _, l := b.call("GET", "/api/sessions", ""); return ids(l) }
	if got := sees(cy); !got[private] || !got[open] || got[teamSession] {
		t.Errorf("cy sees %v", got)
	}
	if got := sees(ann); got[private] || !got[open] || !got[teamSession] {
		t.Errorf("ann sees %v", got)
	}
	if got := sees(ada); got[private] || !got[teamSession] {
		t.Errorf("an admin sees %v; private is cy's alone", got)
	}
	if got := sees(owner); !got[private] {
		t.Errorf("the owner doesn't see %v", got)
	}
	if code, _ := cy.call("GET", "/api/sessions/"+teamSession, ""); code != 404 {
		t.Errorf("cy reads the team session: %d", code)
	}
	if code, _ := cy.call("POST", "/api/sessions/"+teamSession+"/prompts", `{"content":"hi"}`); code != 404 {
		t.Errorf("cy prompts the team session: %d", code)
	}
	if code, _ := ann.call("POST", "/api/sessions/"+teamSession+"/prompts", `{"content":"hi"}`); code != 201 {
		t.Errorf("ann prompts her team's session: %d", code)
	}
	if code, _ := ann.call("POST", "/api/sessions/"+private+"/prompts", `{"content":"hi"}`); code != 404 {
		t.Errorf("ann prompts cy's private session: %d", code)
	}
	if _, l := ann.call("GET", "/api/sessions?team="+web, ""); len(ids(l)) != 1 || !ids(l)[teamSession] {
		t.Errorf("ann's web sessions: %v", ids(l))
	}
	if _, l := ann.call("GET", "/api/sessions?team=workspace", ""); !ids(l)[open] || ids(l)[teamSession] {
		t.Errorf("ann's workspace sessions: %v", ids(l))
	}

	// Visibility: its owner or the team's lead changes it; only the owner makes it private.
	if code, _ := bob.call("PUT", "/api/sessions/"+teamSession+"/visibility", `{"visibility":"workspace"}`); code != 200 {
		t.Errorf("bob opens his session: %d", code)
	}
	if !sees(cy)[teamSession] {
		t.Error("cy can't see the opened session")
	}
	if code, _ := cy.call("POST", "/api/sessions/"+teamSession+"/prompts", `{"content":"hi"}`); code != 403 {
		t.Errorf("cy prompts a workspace-visible team session: %d", code)
	}
	if code, _ := ann.call("PUT", "/api/sessions/"+teamSession+"/visibility", `{"visibility":"private"}`); code != 400 {
		t.Errorf("ann makes bob's session private: %d", code)
	}

	// Team secrets: leads write, members read names, others neither.
	if code, _ := bob.call("PUT", "/api/secrets", `{"scope":"team:`+web+`","name":"K","value":"v"}`); code != 403 {
		t.Errorf("a member sets a team secret: %d", code)
	}
	if code, _ := ann.call("PUT", "/api/secrets", `{"scope":"team:`+web+`","name":"K","value":"v"}`); code != 204 {
		t.Errorf("the lead sets a team secret: %d", code)
	}
	if code, _ := cy.call("GET", "/api/secrets?scope=team:"+web, ""); code != 403 {
		t.Errorf("cy lists the team's secrets: %d", code)
	}
	if _, l := cy.call("GET", "/api/secrets", ""); len(l.([]any)) != 0 {
		t.Errorf("cy sees secrets %v", l)
	}
	if code, _ := ann.call("PUT", "/api/secrets", `{"scope":"","name":"G","value":"v"}`); code != 403 {
		t.Errorf("a lead sets a global secret: %d", code)
	}
	sess, _ := env.DB.Session(t.Context(), teamSession)
	if vars, _ := env.DB.SecretEnv(t.Context(), store.SecretTarget{TeamID: sess.TeamID, EnvironmentID: project, InheritGlobal: true}); vars["K"] != "v" {
		t.Errorf("the team's sessions don't get its secret: %v", vars)
	}

	// The audit log: admins see all, leads their team's.
	if code, l := ann.call("GET", "/api/audit?team="+web, ""); code != 200 || len(l.([]any)) < 3 {
		t.Errorf("ann's team audit: %d %v", code, l)
	}
	if code, _ := bob.call("GET", "/api/audit?team="+web, ""); code != 403 {
		t.Errorf("a member reads the audit: %d", code)
	}

	// Leaving and archiving.
	if code, out := ann.call("DELETE", "/api/teams/web/members/"+idOf["ann"], ""); code != 204 {
		t.Errorf("ann leaves (the owner still leads): %d %v", code, out)
	}
	if code, _ := owner.call("POST", "/api/teams/web/archive", `{"archived":true}`); code != 204 {
		t.Fatal("archive")
	}
	if code, out := bob.call("POST", "/api/sessions", `{"prompt":"more","team_id":"`+web+`"}`); code != 409 {
		t.Errorf("a session for an archived team: %d %v", code, out)
	}
	_ = srv
}

// TestLiveListsHideOthersSessions checks the stream of every session's
// changes only carries what the viewer may read.
func TestLiveListsHideOthersSessions(t *testing.T) {
	_, srv, who := people(t, map[string]string{"bob": "member", "cy": "member"})
	owner, bob, cy := who["owner"], who["bob"], who["cy"]
	_, out := owner.call("POST", "/api/teams", `{"name":"Web"}`)
	web := field(out, "id").(string)
	_, list := owner.call("GET", "/api/people", "")
	for _, p := range list.([]any) {
		if field(p, "name") == "bob" {
			owner.call("PUT", "/api/teams/web/members/"+field(p, "id").(string), `{}`)
		}
	}
	events := func(b *browser) chan string {
		req, _ := http.NewRequest("GET", srv.URL+"/api/events", nil)
		resp, err := b.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		ch := make(chan string, 100)
		go func() {
			sc := bufio.NewScanner(resp.Body)
			for sc.Scan() {
				if line, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
					ch <- line
				}
			}
		}()
		return ch
	}
	cyHears, bobHears := events(cy), events(bob)
	time.Sleep(50 * time.Millisecond)
	_, out = bob.call("POST", "/api/sessions", `{"prompt":"team work","team_id":"`+web+`"}`)
	id := field(out, "session", "id").(string)
	select {
	case <-bobHears:
	case <-time.After(3 * time.Second):
		t.Fatal("bob heard nothing of his session")
	}
	time.Sleep(200 * time.Millisecond)
	for len(cyHears) > 0 {
		if line := <-cyHears; strings.Contains(line, id) {
			t.Errorf("cy heard of a team session: %s", line)
		}
	}
}
