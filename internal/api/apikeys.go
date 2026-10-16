package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/util/ulid"
)

const collAPIKeys = "api_keys"

type APIKeyRecord struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	Name        string     `json:"name"`
	Permissions string     `json:"permissions"`
	Prefix      string     `json:"prefix"`
	Hash        string     `json:"hash"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
}

type APIKeyRepository struct {
	store *store.Store
}

func NewAPIKeyRepository(st *store.Store) *APIKeyRepository {
	if st == nil {
		return nil
	}
	return &APIKeyRepository{store: st}
}

func (r *APIKeyRepository) ListByUser(userID string) ([]*APIKeyRecord, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("api key repository is not configured")
	}
	var all []*APIKeyRecord
	if err := r.store.List(collAPIKeys, &all); err != nil {
		return nil, err
	}
	out := make([]*APIKeyRecord, 0, len(all))
	for _, item := range all {
		if item == nil || item.UserID != userID {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (r *APIKeyRepository) Create(userID, name, permissions string) (string, *APIKeyRecord, error) {
	if r == nil || r.store == nil {
		return "", nil, errors.New("api key repository is not configured")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, errors.New("name is required")
	}
	var err error
	permissions, err = normalizeAPIKeyPermissions(permissions)
	if err != nil {
		return "", nil, err
	}

	token, err := generateAPIKeyToken()
	if err != nil {
		return "", nil, err
	}
	now := time.Now().UTC()
	record := &APIKeyRecord{
		ID:          ulid.New(),
		UserID:      userID,
		Name:        name,
		Permissions: permissions,
		Prefix:      token[:min(12, len(token))],
		Hash:        hashAPIKey(token),
		CreatedAt:   now,
	}
	if err := r.store.Put(collAPIKeys, record.ID, record); err != nil {
		return "", nil, err
	}
	return token, record, nil
}

// ErrAPIKeyNotFound covers both a missing key and someone else's key.
var ErrAPIKeyNotFound = errors.New("api key not found")

func (r *APIKeyRepository) Delete(userID, id string) error {
	if r == nil || r.store == nil {
		return errors.New("api key repository is not configured")
	}
	var record APIKeyRecord
	if err := r.store.Get(collAPIKeys, id, &record); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrAPIKeyNotFound
		}
		return err
	}
	if record.UserID != userID {
		return ErrAPIKeyNotFound
	}
	return r.store.Delete(collAPIKeys, id)
}

func (r *APIKeyRepository) GetByToken(token string) (*APIKeyRecord, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("api key repository is not configured")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("api key is required")
	}
	var all []*APIKeyRecord
	if err := r.store.List(collAPIKeys, &all); err != nil {
		return nil, err
	}
	hash := hashAPIKey(token)
	for _, item := range all {
		if item == nil || item.Hash != hash {
			continue
		}
		return item, nil
	}
	return nil, nil
}

// apiKeyLastUsedResolution is how stale a key's stored last-used time may get
// before a request through that key writes a newer one. Every store write
// rewrites the whole store file (and its .bak), so persisting the timestamp
// on every API-key request turned read traffic into a write per request.
const apiKeyLastUsedResolution = time.Minute

// errLastUsedFresh aborts the UpdateLastUsed transaction without writing.
var errLastUsedFresh = errors.New("api key last-used time is already fresh")

// UpdateLastUsed records that the key was used at usedAt, at most once per
// apiKeyLastUsedResolution: if the stored time is already that recent (or
// newer), nothing is written. A key deleted meanwhile is not brought back.
func (r *APIKeyRepository) UpdateLastUsed(id string, usedAt time.Time) error {
	if r == nil || r.store == nil {
		return errors.New("api key repository is not configured")
	}
	usedAt = usedAt.UTC()
	fresh := func(record *APIKeyRecord) bool {
		return record.LastUsedAt != nil && usedAt.Sub(*record.LastUsedAt) < apiKeyLastUsedResolution
	}

	// The common case, a key used again within the window, costs one read
	// and no write.
	var current APIKeyRecord
	if err := r.store.Get(collAPIKeys, id, &current); err != nil {
		return err
	}
	if fresh(&current) {
		return nil
	}

	// Write inside one transaction against the latest stored record, so a
	// concurrent Delete is not undone and concurrent requests through the
	// same key write once.
	err := r.store.Update(func(tx *store.Tx) error {
		var record APIKeyRecord
		if err := tx.Get(collAPIKeys, id, &record); err != nil {
			return err
		}
		if fresh(&record) {
			return errLastUsedFresh
		}
		record.LastUsedAt = &usedAt
		return tx.Put(collAPIKeys, record.ID, &record)
	})
	if errors.Is(err, errLastUsedFresh) {
		return nil
	}
	return err
}

func generateAPIKeyToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "kervan_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
