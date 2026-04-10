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
	terminate  func()
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	totals   map[string]int64
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		totals:   make(map[string]int64),
	}
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
	m.totals[protocol]++
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

func (m *Manager) AttachTerminator(id string, fn func()) bool {
	if fn == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || s == nil {
		return false
	}
	s.terminate = fn
	return true
}

func (m *Manager) Get(id string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	if !ok || s == nil {
		return nil
	}
	dup := *s
	dup.terminate = nil
	return &dup
}

func (m *Manager) Kill(id string) bool {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if !ok || s == nil {
		return false
	}
	if s.terminate != nil {
		s.terminate()
	}
	return true
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

func (m *Manager) ProtocolStats() (map[string]int, map[string]int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	active := make(map[string]int)
	for _, s := range m.sessions {
		if s == nil {
			continue
		}
		active[s.Protocol]++
	}

	total := make(map[string]int64, len(m.totals))
	for protocol, count := range m.totals {
		total[protocol] = count
	}
	return active, total
}
