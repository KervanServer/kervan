package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var ErrNotFound = errors.New("not found")

// Store is a JSON-file key/value store. The server and CLI commands open the
// same file concurrently, so every write is a locked read-modify-write of the
// file (only the touched key changes) and reads pick up changes written by
// other processes. Without this, a CLI "user create" against a running
// server was invisible to it and was silently erased by the server's next
// write.
type Store struct {
	path       string
	backupPath string
	lockPath   string
	mu         sync.RWMutex
	persistMu  sync.Mutex
	data       map[string]json.RawMessage
	stamp      os.FileInfo // primary file as last loaded or written; guarded by mu
	dirty      bool        // a write failed to persist; guarded by persistMu
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory %s: %w", dataDir, err)
	}
	path := filepath.Join(dataDir, "kervan-store.json")
	s := &Store{
		path:       path,
		backupPath: path + ".bak",
		lockPath:   path + ".lock",
		data:       make(map[string]json.RawMessage),
	}
	if err := s.load(); err != nil {
		return nil, fmt.Errorf("load store from %s: %w", path, err)
	}
	if info, err := os.Stat(path); err == nil {
		s.stamp = info
	}
	return s, nil
}

// Close persists any write that previously failed to reach disk. It never
// rewrites an up-to-date store: a stale snapshot written on Close would undo
// another process's changes.
func (s *Store) Close() error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.mutateLocked(func(map[string]json.RawMessage) {})
}

func (s *Store) Put(collection, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal value for %s/%s: %w", collection, key, err)
	}
	ck := s.composite(collection, key)
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	return s.mutateLocked(func(data map[string]json.RawMessage) { data[ck] = raw })
}

// mutateLocked applies one change as a read-modify-write of the store file
// under the cross-process lock. The caller holds persistMu.
func (s *Store) mutateLocked(apply func(map[string]json.RawMessage)) error {
	unlock, err := lockFile(s.lockPath)
	if err != nil {
		return fmt.Errorf("lock store %s: %w", s.lockPath, err)
	}
	defer unlock()
	s.refresh()
	s.mu.Lock()
	apply(s.data)
	s.mu.Unlock()
	if err := s.flush(); err != nil {
		s.dirty = true
		return err
	}
	s.dirty = false
	return nil
}

// refresh reloads the primary file when another process replaced it since
// it was last loaded or written here. A file that cannot be read keeps the
// in-memory state, which the next write then repairs.
func (s *Store) refresh() {
	info, err := os.Stat(s.path)
	if err != nil {
		return
	}
	s.mu.RLock()
	current := s.stamp != nil && os.SameFile(s.stamp, info) && s.stamp.ModTime().Equal(info.ModTime()) && s.stamp.Size() == info.Size()
	s.mu.RUnlock()
	if current {
		return
	}
	decoded, _, err := loadStoreFile(s.path)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.data = decoded
	s.stamp = info
	s.mu.Unlock()
}

func (s *Store) Get(collection, key string, out any) error {
	s.refresh()
	s.mu.RLock()
	raw, ok := s.data[s.composite(collection, key)]
	s.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode value for %s/%s: %w", collection, key, err)
	}
	return nil
}

func (s *Store) Delete(collection, key string) error {
	ck := s.composite(collection, key)
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	return s.mutateLocked(func(data map[string]json.RawMessage) { delete(data, ck) })
}

func (s *Store) List(collection string, out any) error {
	s.refresh()
	prefix := collection + ":"
	s.mu.RLock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	rows := make([]json.RawMessage, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, s.data[k])
	}
	s.mu.RUnlock()

	joined, err := json.Marshal(rows)
	if err != nil {
		return fmt.Errorf("marshal list for collection %s: %w", collection, err)
	}
	if err := json.Unmarshal(joined, out); err != nil {
		return fmt.Errorf("decode list for collection %s: %w", collection, err)
	}
	return nil
}

func (s *Store) composite(collection, key string) string {
	return collection + ":" + key
}

func (s *Store) load() error {
	decoded, _, err := loadStoreFile(s.path)
	if err == nil {
		s.data = decoded
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		backupDecoded, backupRaw, backupErr := loadStoreFile(s.backupPath)
		if backupErr == nil {
			s.data = backupDecoded
			_ = writeFileAtomically(s.path, backupRaw, 0o600)
			return nil
		}
		return fmt.Errorf("load store: %w; backup recovery failed: %v", err, backupErr)
	}

	backupDecoded, backupRaw, backupErr := loadStoreFile(s.backupPath)
	if backupErr == nil {
		s.data = backupDecoded
		_ = writeFileAtomically(s.path, backupRaw, 0o600)
		return nil
	}
	if errors.Is(backupErr, os.ErrNotExist) {
		return nil
	}
	return backupErr
}

func (s *Store) flush() error {
	snapshot := s.snapshot()
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal store snapshot: %w", err)
	}
	if err := writeFileAtomically(s.path, raw, 0o600); err != nil {
		return fmt.Errorf("write store file %s: %w", s.path, err)
	}
	if info, err := os.Stat(s.path); err == nil {
		s.mu.Lock()
		s.stamp = info
		s.mu.Unlock()
	}
	if err := writeFileAtomically(s.backupPath, raw, 0o600); err != nil {
		return fmt.Errorf("write store backup file %s: %w", s.backupPath, err)
	}
	return nil
}

func (s *Store) snapshot() map[string]json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cloned := make(map[string]json.RawMessage, len(s.data))
	for key, value := range s.data {
		cloned[key] = append(json.RawMessage(nil), value...)
	}
	return cloned
}

func loadStoreFile(path string) (map[string]json.RawMessage, []byte, error) {
	// #nosec G304 -- store file path is resolved from controlled application data directory.
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read store file %s: %w", path, err)
	}
	if len(raw) == 0 {
		return make(map[string]json.RawMessage), raw, nil
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, nil, fmt.Errorf("decode store file %s: %w", path, err)
	}
	return decoded, raw, nil
}
