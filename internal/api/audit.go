package api

import (
	"net/http"
	"strings"

	"github.com/kervanserver/kervan/internal/audit"
)

// emitAudit records an action by the authenticated caller.
// When an admin acts on another user's files the actor is the admin and the
// owner is recorded as meta.target_user.
func (s *Server) emitAudit(r *http.Request, t audit.EventType, p, status, msg string, meta map[string]string) {
	actor := currentUser(r)
	if admin := strings.TrimSpace(r.Header.Get("X-Auth-Actor")); admin != "" {
		if meta == nil {
			meta = map[string]string{}
		}
		if _, set := meta["target_user"]; !set {
			meta["target_user"] = actor
		}
		actor = admin
	}
	s.emitAuditFor(r, actor, t, p, status, msg, meta)
}

// emitAuditFor records an action attributed to username, e.g. a sign-in
// attempt made before any session exists.
func (s *Server) emitAuditFor(r *http.Request, username string, t audit.EventType, p, status, msg string, meta map[string]string) {
	if s.audit == nil {
		return
	}
	protocol := "webui"
	switch {
	case r.Header.Get("X-Auth-Method") == "api-key":
		protocol = "api"
	case isPublicSharePath(r.URL.Path):
		protocol = "share"
	}
	s.audit.Emit(audit.Event{
		Type:     t,
		Username: username,
		Protocol: protocol,
		Path:     p,
		IP:       remoteIP(r.RemoteAddr),
		Status:   status,
		Message:  msg,
		Meta:     meta,
	})
}

func isPublicSharePath(p string) bool {
	return strings.HasPrefix(p, "/api/v1/share/") || strings.HasPrefix(p, "/api/share/")
}
