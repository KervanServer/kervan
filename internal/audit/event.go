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
