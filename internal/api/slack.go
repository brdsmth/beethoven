package api

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/brdsmth/beethoven/internal/slack"
	"github.com/brdsmth/beethoven/internal/store"
)

// Connecting Slack works like connecting GitHub (see github.go): Slack
// returns the browser to /slack/<step>, which redirects to the UI, which
// completes the step through the API under a single-use state. Slack only
// talks to HTTPS addresses it can reach, so the server's URL must be one.

// slackBase checks the server's URL as the browser reaches it, which Slack
// must be able to reach too.
func slackBase(raw string) (string, error) {
	base, ok := baseURL(raw)
	if !ok {
		return "", errors.New("base_url must be the server's origin")
	}
	host, _, err := net.SplitHostPort(strings.TrimPrefix(base, "https://"))
	if err != nil {
		host = strings.TrimPrefix(base, "https://")
	}
	if !strings.HasPrefix(base, "https://") || host == "localhost" || privateIP(host) {
		return "", errors.New("Slack can only reach Beethoven at a public HTTPS address: open Beethoven through that address and try again")
	}
	return base, nil
}

func privateIP(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

func (s *Server) slackStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.Slack.Status(r.Context())
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, st)
}

// createSlackApp creates the server's own Slack app with a person's app
// configuration token, then continues to installing it.
func (s *Server) createSlackApp(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ConfigToken string `json:"config_token"`
		BaseURL     string `json:"base_url"`
	}
	if !decode(w, r, &in) {
		return
	}
	base, err := slackBase(in.BaseURL)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(in.ConfigToken) == "" {
		fail(w, http.StatusBadRequest, "paste an app configuration token")
		return
	}
	if app, _ := s.Slack.App(r.Context()); app != nil {
		fail(w, http.StatusConflict, "a Slack app is already configured")
		return
	}
	app, err := s.Slack.CreateApp(r.Context(), strings.TrimSpace(in.ConfigToken), base)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	reply(w, http.StatusOK, map[string]string{"next": s.slackInstallURL(app, base)})
}

// installSlack returns where to send the browser to add the app to a workspace.
func (s *Server) installSlack(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BaseURL string `json:"base_url"`
	}
	if !decode(w, r, &in) {
		return
	}
	base, err := slackBase(in.BaseURL)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	app, _ := s.Slack.App(r.Context())
	if app == nil {
		fail(w, http.StatusConflict, "no Slack app is configured")
		return
	}
	reply(w, http.StatusOK, map[string]string{"url": s.slackInstallURL(app, base)})
}

func (s *Server) slackInstallURL(app *slack.App, base string) string {
	return s.Slack.InstallURL(app, base+"/slack/installed", s.flows.start("slack", base))
}

// slackInstalled completes an installation.
func (s *Server) slackInstalled(w http.ResponseWriter, r *http.Request) {
	var in struct{ Code, State string }
	if !decode(w, r, &in) {
		return
	}
	base, ok := s.flows.take(in.State, "slack")
	if !ok {
		fail(w, http.StatusBadRequest, "this Slack link has expired or was already used; start again")
		return
	}
	if _, err := s.Slack.Install(r.Context(), in.Code, base+"/slack/installed", base); err != nil {
		if errors.Is(err, store.ErrInvalid) {
			replyErr(w, err)
		} else {
			fail(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	s.slackStatus(w, r)
}

func (s *Server) uninstallSlack(w http.ResponseWriter, r *http.Request) {
	if err := s.Slack.Uninstall(r.Context(), r.PathValue("team")); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) forgetSlackApp(w http.ResponseWriter, r *http.Request) {
	if err := s.Slack.ForgetApp(r.Context()); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteSlackChannel(w http.ResponseWriter, r *http.Request) {
	if err := s.M.Store().DeleteSlackChannel(r.Context(), r.PathValue("team"), r.PathValue("channel")); err != nil {
		replyErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
