package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserDisabled       = errors.New("user disabled")
	ErrUserLocked         = errors.New("user temporarily locked")
)

type Engine struct {
	repo         *UserRepository
	hashAlgo     string
	maxAttempts  int
	lockDuration time.Duration
}

func NewEngine(repo *UserRepository, hashAlgo string, maxAttempts int, lockDuration time.Duration) *Engine {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if lockDuration <= 0 {
		lockDuration = 15 * time.Minute
	}
	return &Engine{
		repo:         repo,
		hashAlgo:     hashAlgo,
		maxAttempts:  maxAttempts,
		lockDuration: lockDuration,
	}
}

func (e *Engine) Authenticate(_ context.Context, username, password string) (*User, error) {
	user, err := e.repo.GetByUsername(username)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrInvalidCredentials
	}
	if !user.Enabled {
		return nil, ErrUserDisabled
	}

	now := time.Now().UTC()
	if user.LockedUntil != nil && user.LockedUntil.After(now) {
		return nil, ErrUserLocked
	}
	if !VerifyPassword(password, user.PasswordHash) {
		user.FailedLogins++
		if user.FailedLogins >= e.maxAttempts {
			until := now.Add(e.lockDuration)
			user.LockedUntil = &until
		}
		_ = e.repo.Update(user)
		return nil, ErrInvalidCredentials
	}

	user.FailedLogins = 0
	user.LockedUntil = nil
	_ = e.repo.UpdateLastLogin(user.ID)
	return user, nil
}

func (e *Engine) AuthenticatePublicKey(_ context.Context, username string, key ssh.PublicKey) (*User, error) {
	user, err := e.repo.GetByUsername(username)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrInvalidCredentials
	}
	if !user.Enabled {
		return nil, ErrUserDisabled
	}

	now := time.Now().UTC()
	if user.LockedUntil != nil && user.LockedUntil.After(now) {
		return nil, ErrUserLocked
	}

	normalized := normalizeAuthorizedKey(ssh.MarshalAuthorizedKey(key))
	for _, authorized := range user.AuthorizedKeys {
		if normalizeAuthorizedKey([]byte(authorized)) == normalized {
			user.FailedLogins = 0
			user.LockedUntil = nil
			_ = e.repo.UpdateLastLogin(user.ID)
			return user, nil
		}
	}
	return nil, ErrInvalidCredentials
}

func (e *Engine) CreateUser(username, password, homeDir string, admin bool) (*User, error) {
	hash, err := HashPassword(password, e.hashAlgo)
	if err != nil {
		return nil, err
	}
	u := &User{
		Username:     username,
		PasswordHash: hash,
		HomeDir:      homeDir,
		Enabled:      true,
		Permissions:  DefaultUserPermissions(),
		Type:         UserTypeVirtual,
	}
	if admin {
		u.Type = UserTypeAdmin
	}
	if err := e.repo.Create(u); err != nil {
		return nil, err
	}
	return u, nil
}

func (e *Engine) ResetPassword(username, password string) error {
	u, err := e.repo.GetByUsername(username)
	if err != nil {
		return err
	}
	if u == nil {
		return ErrInvalidCredentials
	}
	hash, err := HashPassword(password, e.hashAlgo)
	if err != nil {
		return err
	}
	u.PasswordHash = hash
	u.FailedLogins = 0
	u.LockedUntil = nil
	return e.repo.Update(u)
}

func normalizeAuthorizedKey(raw []byte) string {
	return strings.TrimSpace(string(raw))
}
