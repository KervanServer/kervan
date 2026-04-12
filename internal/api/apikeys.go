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

func (r *APIKeyRepository) Delete(userID, id string) error {
	if r == nil || r.store == nil {
		return errors.New("api key repository is not configured")
	}
	var record APIKeyRecord
	if err := r.store.Get(collAPIKeys, id, &record); err != nil {
		return err
	}
	if record.UserID != userID {
		return errors.New("api key not found")
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

func (r *APIKeyRepository) UpdateLastUsed(id string, usedAt time.Time) error {
	if r == nil || r.store == nil {
		return errors.New("api key repository is not configured")
	}
	var record APIKeyRecord
	if err := r.store.Get(collAPIKeys, id, &record); err != nil {
		return err
	}
	usedAt = usedAt.UTC()
	record.LastUsedAt = &usedAt
	return r.store.Put(collAPIKeys, record.ID, &record)
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
