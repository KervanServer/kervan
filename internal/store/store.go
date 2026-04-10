package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	path string
	mu   sync.RWMutex
	data map[string]json.RawMessage
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "kervan-store.json")
	s := &Store{
		path: path,
		data: make(map[string]json.RawMessage),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.flush()
}

func (s *Store) Put(collection, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
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
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	s.data = decoded
	return nil
}

func (s *Store) flush() error {
	s.mu.RLock()
	raw, err := json.MarshalIndent(s.data, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o600)
}
