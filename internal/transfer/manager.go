package transfer

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kervanserver/kervan/internal/util/ulid"
)

type Direction string
type Status string

const (
	DirectionUpload   Direction = "upload"
	DirectionDownload Direction = "download"

	StatusActive    Status = "active"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

type Transfer struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Protocol     string    `json:"protocol"`
	Path         string    `json:"path"`
	Direction    Direction `json:"direction"`
	Status       Status    `json:"status"`
	BytesDone    int64     `json:"bytes_done"`
	BytesTotal   int64     `json:"bytes_total,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	CompletedAt  time.Time `json:"completed_at,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
}

type Stats struct {
	ActiveTransfers int64 `json:"active_transfers"`
	TotalTransfers  int64 `json:"total_transfers"`
	Completed       int64 `json:"completed"`
	Failed          int64 `json:"failed"`
	UploadBytes     int64 `json:"upload_bytes"`
	DownloadBytes   int64 `json:"download_bytes"`
}

type Manager struct {
	mu      sync.RWMutex
	active  map[string]*Transfer
	history []*Transfer
	limit   int

	totalTransfers atomic.Int64
	completed      atomic.Int64
	failed         atomic.Int64
	uploadBytes    atomic.Int64
	downloadBytes  atomic.Int64
}

func NewManager(historyLimit int) *Manager {
	if historyLimit <= 0 {
		historyLimit = 1000
	}
	return &Manager{
		active:  make(map[string]*Transfer),
		history: make([]*Transfer, 0, historyLimit),
		limit:   historyLimit,
	}
}

func (m *Manager) Start(username, protocol, path string, direction Direction, totalBytes int64) string {
	id := ulid.New()
	tr := &Transfer{
		ID:         id,
		Username:   username,
		Protocol:   protocol,
		Path:       path,
		Direction:  direction,
		Status:     StatusActive,
		BytesTotal: totalBytes,
		StartedAt:  time.Now().UTC(),
	}
	m.mu.Lock()
	m.active[id] = tr
	m.mu.Unlock()
	m.totalTransfers.Add(1)
	return id
}

func (m *Manager) AddBytes(id string, n int64) {
	if n <= 0 {
		return
	}
	m.mu.Lock()
	tr := m.active[id]
	if tr != nil {
		tr.BytesDone += n
	}
	m.mu.Unlock()
	if tr == nil {
		return
	}
	if tr.Direction == DirectionUpload {
		m.uploadBytes.Add(n)
	} else {
		m.downloadBytes.Add(n)
	}
}

func (m *Manager) End(id string, status Status, err string) {
	m.mu.Lock()
	tr := m.active[id]
	if tr == nil {
		m.mu.Unlock()
		return
	}
	delete(m.active, id)
	tr.Status = status
	tr.ErrorMessage = err
	tr.CompletedAt = time.Now().UTC()
	m.history = append(m.history, tr)
	if len(m.history) > m.limit {
		m.history = m.history[len(m.history)-m.limit:]
	}
	m.mu.Unlock()

	switch status {
	case StatusCompleted:
		m.completed.Add(1)
	case StatusFailed:
		m.failed.Add(1)
	}
}

func (m *Manager) Active() []*Transfer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Transfer, 0, len(m.active))
	for _, tr := range m.active {
		cp := *tr
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}

func (m *Manager) Recent(limit int) []*Transfer {
	if limit <= 0 {
		limit = 50
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit > len(m.history) {
		limit = len(m.history)
	}
	out := make([]*Transfer, 0, limit)
	for i := len(m.history) - 1; i >= 0 && len(out) < limit; i-- {
		cp := *m.history[i]
		out = append(out, &cp)
	}
	return out
}

func (m *Manager) Stats() Stats {
	m.mu.RLock()
	activeCount := len(m.active)
	m.mu.RUnlock()
	return Stats{
		ActiveTransfers: int64(activeCount),
		TotalTransfers:  m.totalTransfers.Load(),
		Completed:       m.completed.Load(),
		Failed:          m.failed.Load(),
		UploadBytes:     m.uploadBytes.Load(),
		DownloadBytes:   m.downloadBytes.Load(),
	}
}
