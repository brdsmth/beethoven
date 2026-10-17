package api

import (
	"net/http"
	"strings"

	"github.com/brdsmth/beethoven/internal/access"
	"github.com/brdsmth/beethoven/internal/capability"
)

// listCapabilities is the catalog of toolchains sandboxes can be given.
func (s *Server) listCapabilities(w http.ResponseWriter, r *http.Request) {
	reply(w, http.StatusOK, capability.Catalog)
}

// addCapability installs a capability in a session's sandbox now and keeps
// it for the session's later sandboxes.
func (s *Server) addCapability(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if _, ok := s.session(w, r, access.PromptSession, "add capabilities to this session"); !ok {
		return
	}
	added, err := s.M.AddCapability(r.Context(), r.PathValue("id"), strings.TrimSpace(in.ID), "web")
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, added)
}
