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

	// The uniqueness check and both writes share one transaction, so
	// concurrent creators (including another process) cannot both pass.
	return r.store.Update(func(tx *store.Tx) error {
		existing, err := userByNameTx(tx, user.Username)
		if err != nil {
			return err
		}
		if existing != nil {
			return fmt.Errorf("username %q already exists", user.Username)
		}
		now := time.Now().UTC()
		user.CreatedAt = now
		user.UpdatedAt = now
		if err := tx.Put(collUsers, user.ID, user); err != nil {
			return err
		}
		return tx.Put(collUserByName, usernameIndexKey(user.Username), user.ID)
	})
}

func userByIDTx(tx *store.Tx, id string) (*User, error) {
	var user User
	if err := tx.Get(collUsers, id, &user); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

// userByNameTx mirrors GetByUsername, including the legacy un-normalized
// index key and treating an index entry without a user record as absent.
func userByNameTx(tx *store.Tx, username string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, nil
	}
	var id string
	for _, key := range []string{usernameIndexKey(username), username} {
		err := tx.Get(collUserByName, key, &id)
		if err == nil {
			return userByIDTx(tx, id)
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	return nil, nil
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
	return r.store.Update(func(tx *store.Tx) error {
		return updateTx(tx, user)
	})
}

// updateTx writes user over the stored record and keeps the username index
// in step, all within the caller's transaction.
func updateTx(tx *store.Tx, user *User) error {
	existing, err := userByIDTx(tx, user.ID)
	if err != nil {
		return err
	}
	if existing == nil {
		return errors.New("user not found")
	}
	renamed := !strings.EqualFold(existing.Username, user.Username)
	if renamed {
		other, err := userByNameTx(tx, user.Username)
		if err != nil {
			return err
		}
		if other != nil && other.ID != user.ID {
			return fmt.Errorf("username %q already exists", user.Username)
		}
	}
	user.UpdatedAt = time.Now().UTC()
	if err := tx.Put(collUsers, user.ID, user); err != nil {
		return err
	}
	if renamed {
		for _, key := range usernameIndexKeys(existing.Username) {
			tx.Delete(collUserByName, key)
		}
	}
	return tx.Put(collUserByName, usernameIndexKey(user.Username), user.ID)
}

// Mutate applies fn to the latest stored copy of the user and saves the
// result in one transaction, so fields fn does not touch keep any change
// made concurrently. It returns the saved user, or nil if it does not exist.
// An error from fn aborts the write.
func (r *UserRepository) Mutate(id string, fn func(*User) error) (*User, error) {
	var saved *User
	err := r.store.Update(func(tx *store.Tx) error {
		u, err := userByIDTx(tx, id)
		if err != nil || u == nil {
			return err
		}
		if err := fn(u); err != nil {
			return err
		}
		if err := updateTx(tx, u); err != nil {
			return err
		}
		saved = u
		return nil
	})
	return saved, err
}

func (r *UserRepository) Delete(id string) error {
	// The record and its index entries go together: a crash or a concurrent
	// rename between separate deletes left a dangling index entry.
	return r.store.Update(func(tx *store.Tx) error {
		user, err := userByIDTx(tx, id)
		if err != nil || user == nil {
			return err
		}
		for _, key := range usernameIndexKeys(user.Username) {
			tx.Delete(collUserByName, key)
		}
		tx.Delete(collUsers, id)
		return nil
	})
}

func (r *UserRepository) List() ([]*User, error) {
	var users []*User
	if err := r.store.List(collUsers, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func (r *UserRepository) UpdateLastLogin(id string) error {
	_, err := r.Mutate(id, func(u *User) error {
		now := time.Now().UTC()
		u.LastLoginAt = &now
		u.FailedLogins = 0
		u.LockedUntil = nil
		return nil
	})
	return err
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
