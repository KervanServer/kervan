package server

import (
	"context"
	"time"

	"github.com/kervanserver/kervan/internal/audit"
)

const accessSweepInterval = 30 * time.Second

// runAccessSweeper ends FTP/SFTP/SCP sessions whose user no longer passes
// the sign-in policy: deleted, disabled, expired, or connected from an
// address the user's allow list no longer covers. Sign-in checks alone
// would let such a connection run on indefinitely.
func (a *App) runAccessSweeper(ctx context.Context) {
	ticker := time.NewTicker(accessSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			a.revokeIneligibleSessions(now)
			// Quota trackers are keyed by user ID, so a re-created account
			// never inherits one; this only frees deleted accounts' entries.
			a.pruneQuotaTrackers()
		}
	}
}

func (a *App) revokeIneligibleSessions(now time.Time) int {
	revoked := 0
	for _, sess := range a.sessions.List() {
		user, err := a.authRepo.GetByUsername(sess.Username)
		if err != nil {
			continue // transient store error: do not cut a session over it
		}
		reason := ""
		switch {
		case user == nil:
			reason = "account deleted"
		case !user.Enabled:
			reason = "account disabled"
		case user.Expired(now):
			reason = "account expired"
		case !user.IPAllowed(sess.RemoteAddr):
			reason = "address no longer allowed"
		default:
			continue
		}
		if !a.sessions.Kill(sess.ID) {
			continue
		}
		revoked++
		if a.logger != nil {
			a.logger.Info("session revoked", "user", sess.Username, "protocol", sess.Protocol, "remote_addr", sess.RemoteAddr, "reason", reason)
		}
		if a.audit != nil {
			a.audit.Emit(audit.Event{
				Type:     audit.EventSessionRevoked,
				Username: sess.Username,
				Protocol: sess.Protocol,
				IP:       sess.RemoteAddr,
				Status:   "ok",
				Message:  reason,
				Meta:     map[string]string{"session_id": sess.ID},
			})
		}
	}
	return revoked
}
