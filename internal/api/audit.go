package api

import (
	"net/http"
	"strings"

	"github.com/kervanserver/kervan/internal/audit"
)

// emitAudit records an action by the authenticated caller.
func (s *Server) emitAudit(r *http.Request, t audit.EventType, p, status, msg string, meta map[string]string) {
	s.emitAuditFor(r, currentUser(r), t, p, status, msg, meta)
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
