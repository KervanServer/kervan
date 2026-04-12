package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/util/ulid"
)

const (
	collUsers      = "users"
	collUserByName = "users_idx_username"
)

type UserRepository struct {
	store *store.Store
}

func NewUserRepository(s *store.Store) *UserRepository {
	return &UserRepository{store: s}
}

func (r *UserRepository) Create(user *User) error {
	user.Username = strings.TrimSpace(user.Username)
	if user.Username == "" {
		return errors.New("username is required")
	}
	if user.ID == "" {
		user.ID = ulid.New()
	}
	if user.Type == "" {
		user.Type = UserTypeVirtual
	}
	if strings.TrimSpace(user.AuthProvider) == "" {
		user.AuthProvider = AuthProviderLocal
	}
	if permissionsEmpty(user.Permissions) {
		user.Permissions = DefaultUserPermissions()
	}

	if existing, _ := r.GetByUsername(user.Username); existing != nil {
		return fmt.Errorf("username %q already exists", user.Username)
	}

	now := time.Now().UTC()
	user.CreatedAt = now
	user.UpdatedAt = now

	if err := r.store.Put(collUsers, user.ID, user); err != nil {
		return err
	}
	return r.store.Put(collUserByName, usernameIndexKey(user.Username), user.ID)
}

func permissionsEmpty(p UserPermissions) bool {
	return !p.Upload &&
		!p.Download &&
		!p.Delete &&
		!p.Rename &&
		!p.CreateDir &&
		!p.ListDir &&
		!p.Chmod &&
		p.MaxFileSize == 0 &&
		len(p.AllowedExt) == 0 &&
		len(p.DeniedExt) == 0
}

func (r *UserRepository) GetByID(id string) (*User, error) {
	var user User
	if err := r.store.Get(collUsers, id, &user); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (r *UserRepository) GetByUsername(username string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, nil
	}
	var id string
	if err := r.store.Get(collUserByName, usernameIndexKey(username), &id); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if err := r.store.Get(collUserByName, username, &id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, nil
			}
			return nil, err
		}
	}
	return r.GetByID(id)
}

func (r *UserRepository) Update(user *User) error {
	if user == nil || user.ID == "" {
		return errors.New("user id is required")
	}
	user.Username = strings.TrimSpace(user.Username)
	if user.Username == "" {
		return errors.New("username is required")
	}
	existing, err := r.GetByID(user.ID)
	if err != nil {
		return err
	}
	if existing == nil {
		return errors.New("user not found")
	}
	if !strings.EqualFold(existing.Username, user.Username) {
		other, err := r.GetByUsername(user.Username)
		if err != nil {
			return err
		}
		if other != nil && other.ID != user.ID {
			return fmt.Errorf("username %q already exists", user.Username)
		}
	}
	user.UpdatedAt = time.Now().UTC()
	if err := r.store.Put(collUsers, user.ID, user); err != nil {
		return err
	}
	if !strings.EqualFold(existing.Username, user.Username) {
		for _, key := range usernameIndexKeys(existing.Username) {
			_ = r.store.Delete(collUserByName, key)
		}
	}
	return r.store.Put(collUserByName, usernameIndexKey(user.Username), user.ID)
}

func (r *UserRepository) Delete(id string) error {
	user, err := r.GetByID(id)
	if err != nil {
		return err
	}
	if user == nil {
		return nil
	}
	for _, key := range usernameIndexKeys(user.Username) {
		_ = r.store.Delete(collUserByName, key)
	}
	return r.store.Delete(collUsers, id)
}

func (r *UserRepository) List() ([]*User, error) {
	var users []*User
	if err := r.store.List(collUsers, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func (r *UserRepository) UpdateLastLogin(id string) error {
	u, err := r.GetByID(id)
	if err != nil || u == nil {
		return err
	}
	now := time.Now().UTC()
	u.LastLoginAt = &now
	u.FailedLogins = 0
	u.LockedUntil = nil
	return r.Update(u)
}

func usernameIndexKey(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func usernameIndexKeys(username string) []string {
	trimmed := strings.TrimSpace(username)
	if trimmed == "" {
		return nil
	}
	normalized := usernameIndexKey(trimmed)
	if normalized == trimmed {
		return []string{normalized}
	}
	return []string{normalized, trimmed}
}
