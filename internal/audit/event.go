package audit

import "time"

type EventType string

const (
	EventAuthSuccess EventType = "auth.success"
	EventAuthFailure EventType = "auth.failure"
	EventFileRead    EventType = "file.read"
	EventFileWrite   EventType = "file.write"
	EventFileDelete  EventType = "file.delete"
	// EventConnectionRejected records a connection refused by an admission
	// control such as max_connections.
	EventConnectionRejected EventType = "connection.rejected"

	EventFileRename EventType = "file.rename"
	EventDirCreate  EventType = "dir.create"
	EventDirDelete  EventType = "dir.delete"

	EventPasswordChange EventType = "auth.password_change"

	// Administrative changes made through the WebUI/API.
	EventAdminUserCreate   EventType = "admin.user.create"
	EventAdminUserUpdate   EventType = "admin.user.update"
	EventAdminUserDelete   EventType = "admin.user.delete"
	EventAdminUserExport   EventType = "admin.user.export"
	EventAdminGroupCreate  EventType = "admin.group.create"
	EventAdminGroupUpdate  EventType = "admin.group.update"
	EventAdminGroupDelete  EventType = "admin.group.delete"
	EventAdminAPIKeyCreate EventType = "admin.apikey.create"
	EventAdminAPIKeyDelete EventType = "admin.apikey.delete"
	EventAdminConfigUpdate EventType = "admin.config.update"
	EventAdminConfigReload EventType = "admin.config.reload"
	EventAdminSessionKill  EventType = "admin.session.kill"
	// EventSessionRevoked records a connection ended because its user no
	// longer passes the sign-in policy (expired, disabled, deleted, or an
	// address outside the user's allow list).
	EventSessionRevoked EventType = "session.revoked"
	// Share-link lifecycle and public downloads. Download events are
	// attributed to the link owner, with the requester's address in IP.
	EventShareCreate   EventType = "share.create"
	EventShareRevoke   EventType = "share.revoke"
	EventShareDownload EventType = "share.download"
)

type Event struct {
	ID        string            `json:"id"`
	Timestamp time.Time         `json:"timestamp"`
	Type      EventType         `json:"type"`
	Username  string            `json:"username,omitempty"`
	Protocol  string            `json:"protocol,omitempty"`
	Path      string            `json:"path,omitempty"`
	IP        string            `json:"ip,omitempty"`
	Status    string            `json:"status,omitempty"`
	Message   string            `json:"message,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}
