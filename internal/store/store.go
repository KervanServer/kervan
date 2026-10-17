package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

var ErrNotFound = errors.New("not found")

// Backend names accepted by OpenWithOptions and KERVAN_STORE_BACKEND.
const (
	// BackendJSON is the default: one kervan-store.json (plus .bak) under
	// the data directory, rewritten in full on every write.
	BackendJSON = "json"
	// BackendSQLite stores the same records in kervan-store.db (SQLite in
	// WAL mode). It is not the default yet; see .project/STORE-DESIGN.md.
	BackendSQLite = "sqlite"
)

// backendEnv selects the backend when Options.Backend is empty. It exists so
// the whole test suite can run against either backend without code changes;
// it is not a supported configuration knob yet.
const backendEnv = "KERVAN_STORE_BACKEND"

// Options configures Open.
type Options struct {
	// Backend is BackendJSON (default) or BackendSQLite.
	Backend string
}

// backend is the storage engine behind Store. Store does the JSON encoding
// of values, the composite collection:key naming used by the JSON file, and
// the ErrInsideUpdate guard; a backend only moves raw values.
type backend interface {
	get(collection, key string) (json.RawMessage, bool, error)
	put(collection, key string, raw json.RawMessage) error
	del(collection, key string) error
	// list returns a collection's values ordered by key.
	list(collection string) ([]json.RawMessage, error)
	// update runs fn in one transaction; fn's error rolls it back.
	update(fn func(txBackend) error) error
	close() error
}

// txBackend is a backend's view inside update. Reads see the transaction's
// own writes.
type txBackend interface {
	get(collection, key string) (json.RawMessage, bool, error)
	put(collection, key string, raw json.RawMessage) error
	del(collection, key string) error
}

// Store is the key/value store for users, groups, API keys, share links and
// system secrets. The server and CLI commands open the same store at the
// same time, so every backend makes writes from one process visible to the
// other and never loses them.
type Store struct {
	b       backend
	kind    string
	txOwner atomic.Uint64 // goroutine running an Update callback, 0 if none
}

// Open opens the store in dataDir with the default backend (JSON, unless
// KERVAN_STORE_BACKEND says otherwise).
func Open(dataDir string) (*Store, error) {
	return OpenWithOptions(dataDir, Options{})
}

// OpenWithOptions opens the store in dataDir with the given options.
func OpenWithOptions(dataDir string, opts Options) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory %s: %w", dataDir, err)
	}
	kind := ResolveBackend(opts.Backend)
	var (
		b   backend
		err error
	)
	switch kind {
	case BackendJSON:
		b, err = openJSONBackend(dataDir)
	case BackendSQLite:
		b, err = openSQLiteBackend(dataDir)
	default:
		return nil, fmt.Errorf("unknown store backend %q (want %q or %q)", kind, BackendJSON, BackendSQLite)
	}
	if err != nil {
		return nil, err
	}
	return &Store{b: b, kind: kind}, nil
}

// ResolveBackend returns the backend Open would use for the given option:
// the option itself, else KERVAN_STORE_BACKEND, else BackendJSON. The name
// is normalised but not validated.
func ResolveBackend(option string) string {
	kind := strings.ToLower(strings.TrimSpace(option))
	if kind == "" {
		kind = strings.ToLower(strings.TrimSpace(os.Getenv(backendEnv)))
	}
	if kind == "" {
		kind = BackendJSON
	}
	return kind
}

// FileName returns the name of the store's main file in the data directory
// for a backend: kervan-store.json or kervan-store.db.
func FileName(backend string) string {
	if ResolveBackend(backend) == BackendSQLite {
		return sqliteFileName
	}
	return "kervan-store.json"
}

// Backend reports which backend the store uses.
func (s *Store) Backend() string { return s.kind }

// Path returns the store's main file.
func (s *Store) Path() string {
	if p, ok := s.b.(interface{ filePath() string }); ok {
		return p.filePath()
	}
	return ""
}

// ErrSnapshotUnsupported is returned by Snapshot for backends whose files
// are copied directly instead (the JSON backend replaces its file
// atomically on every write, so a plain copy is already consistent).
var ErrSnapshotUnsupported = errors.New("store: backend does not take snapshots")

// Snapshot writes a consistent copy of the whole store to dst, which must
// not exist, while the store stays in use (other processes included).
func (s *Store) Snapshot(dst string) error {
	if err := s.checkNotInUpdate(); err != nil {
		return err
	}
	sn, ok := s.b.(interface{ snapshot(string) error })
	if !ok {
		return ErrSnapshotUnsupported
	}
	return sn.snapshot(dst)
}

// Close releases the store. For the JSON backend it also persists a write
// that previously failed to reach disk.
func (s *Store) Close() error {
	if err := s.checkNotInUpdate(); err != nil {
		return err
	}
	return s.b.close()
}

func (s *Store) Get(collection, key string, out any) error {
	if err := s.checkNotInUpdate(); err != nil {
		return err
	}
	raw, ok, err := s.b.get(collection, key)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return decodeValue(collection, key, raw, out)
}

func (s *Store) Put(collection, key string, value any) error {
	if err := s.checkNotInUpdate(); err != nil {
		return err
	}
	raw, err := encodeValue(collection, key, value)
	if err != nil {
		return err
	}
	return s.b.put(collection, key, raw)
}

func (s *Store) Delete(collection, key string) error {
	if err := s.checkNotInUpdate(); err != nil {
		return err
	}
	return s.b.del(collection, key)
}

// List decodes every value in collection, ordered by key, into out (a
// pointer to a slice).
func (s *Store) List(collection string, out any) error {
	if err := s.checkNotInUpdate(); err != nil {
		return err
	}
	rows, err := s.b.list(collection)
	if err != nil {
		return err
	}
	if rows == nil {
		rows = []json.RawMessage{}
	}
	joined, err := json.Marshal(rows)
	if err != nil {
		return fmt.Errorf("marshal list for collection %s: %w", collection, err)
	}
	if err := json.Unmarshal(joined, out); err != nil {
		return fmt.Errorf("decode list for collection %s: %w", collection, err)
	}
	return nil
}

// Tx is the view of the store inside Update. Reads see the transaction's own
// writes; nothing is visible to other readers until Update commits.
type Tx struct {
	b txBackend
}

func (tx *Tx) Get(collection, key string, out any) error {
	raw, ok, err := tx.b.get(collection, key)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return decodeValue(collection, key, raw, out)
}

func (tx *Tx) Put(collection, key string, value any) error {
	raw, err := encodeValue(collection, key, value)
	if err != nil {
		return err
	}
	return tx.b.put(collection, key, raw)
}

// Delete removes a key. A backend error is reported when Update commits.
func (tx *Tx) Delete(collection, key string) {
	if err := tx.b.del(collection, key); err != nil {
		if r, ok := tx.b.(interface{ fail(error) }); ok {
			r.fail(err)
		}
	}
}

// Update runs fn as one read-check-write transaction against the latest
// committed state, under the cross-process lock. Its writes are committed
// together; if fn returns an error nothing changes. fn must use only tx:
// Store methods called from fn's goroutine return ErrInsideUpdate.
func (s *Store) Update(fn func(tx *Tx) error) error {
	if err := s.checkNotInUpdate(); err != nil {
		return err
	}
	return s.b.update(func(tb txBackend) error {
		return s.runTx(fn, &Tx{b: tb})
	})
}

func encodeValue(collection, key string, value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal value for %s/%s: %w", collection, key, err)
	}
	return raw, nil
}

func decodeValue(collection, key string, raw json.RawMessage, out any) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode value for %s/%s: %w", collection, key, err)
	}
	return nil
}
