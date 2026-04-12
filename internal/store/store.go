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

type Store struct {
	path       string
	backupPath string
	mu         sync.RWMutex
	persistMu  sync.Mutex
	data       map[string]json.RawMessage
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "kervan-store.json")
	s := &Store{
		path:       path,
		backupPath: path + ".bak",
		data:       make(map[string]json.RawMessage),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	return s.flush()
}

func (s *Store) Put(collection, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.Lock()
	s.data[s.composite(collection, key)] = raw
	s.mu.Unlock()
	return s.flush()
}

func (s *Store) Get(collection, key string, out any) error {
	s.mu.RLock()
	raw, ok := s.data[s.composite(collection, key)]
	s.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	return json.Unmarshal(raw, out)
}

func (s *Store) Delete(collection, key string) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.Lock()
	delete(s.data, s.composite(collection, key))
	s.mu.Unlock()
	return s.flush()
}

func (s *Store) List(collection string, out any) error {
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
		return err
	}
	return json.Unmarshal(joined, out)
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
		return err
	}
	if err := writeFileAtomically(s.path, raw, 0o600); err != nil {
		return err
	}
	return writeFileAtomically(s.backupPath, raw, 0o600)
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
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) == 0 {
		return make(map[string]json.RawMessage), raw, nil
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, nil, err
	}
	return decoded, raw, nil
}
