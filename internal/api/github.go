package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"

	"github.com/brdsmth/beethoven/internal/github"
	"github.com/brdsmth/beethoven/internal/store"
)

// Connecting GitHub takes the browser to github.com and back up to three
// times: to register the server's app (unless one is preconfigured), to
// authorize it, and to install it on repositories. GitHub returns the
// browser to /github/<step>, which carries no API token, so the server only
// redirects it to the UI; the UI completes the step through the API, and a
// single-use state ties each return to the request that started it.

// flows are the GitHub round trips in progress, by state.
type flows struct {
	mu sync.Mutex
	m  map[string]flow
}

type flow struct {
	kind    string // app or authorize
	base    string // the server's URL as the browser reaches it
	expires time.Time
}

func (f *flows) start(kind, base string) string {
	b := make([]byte, 16)
	rand.Read(b)
	state := hex.EncodeToString(b)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]flow{}
	}
	for k, v := range f.m {
		if time.Now().After(v.expires) {
			delete(f.m, k)
		}
	}
	f.m[state] = flow{kind: kind, base: base, expires: time.Now().Add(30 * time.Minute)}
	return state
}

func (f *flows) take(state, kind string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.m[state]
	delete(f.m, state)
	return v.base, ok && v.kind == kind && time.Now().Before(v.expires)
}

var ownerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)

// baseURL checks the server's URL as the browser reaches it: an origin.
func baseURL(s string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") ||
		u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

// githubReturn sends the browser GitHub returned from to the UI, with
// GitHub's parameters and the step.
func githubReturn(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	q.Set("github", r.PathValue("step"))
	http.Redirect(w, r, "/?"+q.Encode(), http.StatusFound)
}

func (s *Server) githubStatus(w http.ResponseWriter, r *http.Request) {
	reply(w, http.StatusOK, s.GitHub.Status(r.Context()))
}

// registerApp starts registering the server's own GitHub App: it returns
// the manifest for the browser to post to GitHub, and where to post it.
func (s *Server) registerApp(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BaseURL string `json:"base_url"`
		Org     string `json:"org"`    // the organization to own the app; "" for the person's account
		Public  bool   `json:"public"` // let other accounts install it
	}
	if !decode(w, r, &in) {
		return
	}
	base, ok := baseURL(in.BaseURL)
	switch {
	case !ok:
		fail(w, http.StatusBadRequest, "base_url must be the server's origin, like https://beethoven.example.com")
		return
	case in.Org != "" && !ownerName.MatchString(in.Org):
		fail(w, http.StatusBadRequest, "invalid organization name")
		return
	}
	if app, _ := s.GitHub.App(r.Context()); app != nil {
		fail(w, http.StatusConflict, "a GitHub App is already configured")
		return
	}
	manifest, _ := json.Marshal(github.Manifest(base, in.Public))
	reply(w, http.StatusOK, map[string]string{
		"url": s.GitHub.ManifestURL(in.Org, s.flows.start("app", base)), "manifest": string(manifest),
	})
}

// appCreated completes a registration and continues to authorizing the app.
func (s *Server) appCreated(w http.ResponseWriter, r *http.Request) {
	var in struct{ Code, State string }
	if !decode(w, r, &in) {
		return
	}
	base, ok := s.flows.take(in.State, "app")
	if !ok {
		fail(w, http.StatusBadRequest, "this GitHub link has expired or was already used; start again")
		return
	}
	app, err := s.GitHub.RegisterApp(r.Context(), in.Code)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	reply(w, http.StatusOK, map[string]string{"next": s.authorizeURL(app, base)})
}

// authorize returns where to send the browser to authorize the app.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BaseURL string `json:"base_url"`
	}
	if !decode(w, r, &in) {
		return
	}
	base, ok := baseURL(in.BaseURL)
	if !ok {
		fail(w, http.StatusBadRequest, "base_url must be the server's origin")
		return
	}
	app, _ := s.GitHub.App(r.Context())
	if app == nil {
		fail(w, http.StatusConflict, "no GitHub App is configured")
		return
	}
	reply(w, http.StatusOK, map[string]string{"url": s.authorizeURL(app, base)})
}

func (s *Server) authorizeURL(app *github.App, base string) string {
	return s.GitHub.AuthorizeURL(app, base+"/github/authorized", s.flows.start("authorize", base))
}

// connectAccount completes an authorization. Its reply says where to send
// the browser next: to install the app, if it is installed nowhere the
// person can reach yet.
func (s *Server) connectAccount(w http.ResponseWriter, r *http.Request) {
	var in struct{ Code, State string }
	if !decode(w, r, &in) {
		return
	}
	base, ok := s.flows.take(in.State, "authorize")
	if !ok {
		fail(w, http.StatusBadRequest, "this GitHub link has expired or was already used; start again")
		return
	}
	if _, err := s.GitHub.Connect(r.Context(), in.Code, base+"/github/authorized"); err != nil {
		if errors.Is(err, store.ErrInvalid) {
			replyErr(w, err)
		} else {
			fail(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	st := s.GitHub.Status(r.Context())
	next := ""
	if st.App != nil && st.Error == "" && len(st.Installations) == 0 {
		next = st.App.InstallURL
	}
	reply(w, http.StatusOK, map[string]any{"status": st, "next": next})
}

func (s *Server) disconnectAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.GitHub.Disconnect(r.Context()); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) forgetApp(w http.ResponseWriter, r *http.Request) {
	if err := s.GitHub.ForgetApp(r.Context()); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
