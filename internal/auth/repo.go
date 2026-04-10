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
	if !user.Enabled {
		user.Enabled = true
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
	return r.store.Put(collUserByName, user.Username, user.ID)
}

func permissionsEmpty(p UserPermissions) bool {
	return !p.Upload &&
		!p.Download &&
		!p.Delete &&
		!p.Rename &&
		!p.CreateDir &&
		!p.ListDir &&
		!p.Chmod &&
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
	var id string
	if err := r.store.Get(collUserByName, username, &id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return r.GetByID(id)
}

func (r *UserRepository) Update(user *User) error {
	if user == nil || user.ID == "" {
		return errors.New("user id is required")
	}
	user.UpdatedAt = time.Now().UTC()
	return r.store.Put(collUsers, user.ID, user)
}

func (r *UserRepository) Delete(id string) error {
	user, err := r.GetByID(id)
	if err != nil {
		return err
	}
	if user == nil {
		return nil
	}
	_ = r.store.Delete(collUserByName, user.Username)
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
