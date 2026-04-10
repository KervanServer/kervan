package session

import (
	"sync"
	"time"

	"github.com/kervanserver/kervan/internal/util/ulid"
)

type Session struct {
	ID         string    `json:"id"`
	Username   string    `json:"username"`
	Protocol   string    `json:"protocol"`
	RemoteAddr string    `json:"remote_addr"`
	StartedAt  time.Time `json:"started_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*Session)}
}

func (m *Manager) Start(username, protocol, remoteAddr string) *Session {
	now := time.Now().UTC()
	s := &Session{
		ID:         ulid.New(),
		Username:   username,
		Protocol:   protocol,
		RemoteAddr: remoteAddr,
		StartedAt:  now,
		LastSeenAt: now,
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	return s
}

func (m *Manager) Touch(id string) {
	m.mu.Lock()
	if s, ok := m.sessions[id]; ok {
		s.LastSeenAt = time.Now().UTC()
	}
	m.mu.Unlock()
}

func (m *Manager) End(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

func (m *Manager) List() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		dup := *s
		out = append(out, &dup)
	}
	return out
}
