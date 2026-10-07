package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserDisabled       = errors.New("user disabled")
	ErrUserLocked         = errors.New("user temporarily locked")
)

const (
	AuthProviderLocal = "local"
	AuthProviderLDAP  = "ldap"
)

type Engine struct {
	// policyMu guards the policy fields below, which config reload updates
	// while logins are in flight.
	policyMu          sync.RWMutex
	repo              *UserRepository
	hashAlgo          string
	maxAttempts       int
	lockDuration      time.Duration
	minPasswordLength int
	requireSpecial    bool
	lockoutDisabled   bool
	ldap              *LDAPProvider
	ipThrottle        *IPThrottle
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

func (e *Engine) Authenticate(ctx context.Context, username, password string) (*User, error) {
	return e.authenticate(ctx, username, password)
}

func (e *Engine) authenticate(ctx context.Context, username, password string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, ErrInvalidCredentials
	}
	user, err := e.repo.GetByUsername(username)
	if err != nil {
		return nil, err
	}

	if user != nil && !isLDAPUser(user) {
		return e.authenticateLocalUser(user, password)
	}

	if e.ldap != nil {
		return e.authenticateLDAPUser(ctx, user, username, password)
	}

	if user == nil {
		return nil, ErrInvalidCredentials
	}
	return e.authenticateLocalUser(user, password)
}

func (e *Engine) authenticateLocalUser(user *User, password string) (*User, error) {
	if err := e.ensureLoginAllowed(user); err != nil {
		return nil, err
	}
	// External (OIDC) accounts have no password; refuse explicitly rather
	// than relying on the unusable hash.
	if strings.EqualFold(user.AuthProvider, AuthProviderOIDC) {
		return nil, ErrInvalidCredentials
	}
	if !VerifyPassword(password, user.PasswordHash) {
		_ = e.registerFailedLogin(user)
		return nil, ErrInvalidCredentials
	}
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

	for _, authorized := range user.AuthorizedKeys {
		if authorizedKeyMatches(authorized, key) {
			user.FailedLogins = 0
			user.LockedUntil = nil
			_ = e.repo.UpdateLastLogin(user.ID)
			return user, nil
		}
	}
	return nil, ErrInvalidCredentials
}

func (e *Engine) SetLDAPProvider(provider *LDAPProvider) {
	e.ldap = provider
}

// SetLockoutEnabled toggles per-account lockout after max_attempts failures
// (security.brute_force.enabled).
func (e *Engine) SetLockoutEnabled(enabled bool) {
	e.policyMu.Lock()
	defer e.policyMu.Unlock()
	e.lockoutDisabled = !enabled
}

// SetLockoutPolicy updates the per-account lockout thresholds at runtime.
func (e *Engine) SetLockoutPolicy(maxAttempts int, lockDuration time.Duration) {
	e.policyMu.Lock()
	defer e.policyMu.Unlock()
	if maxAttempts > 0 {
		e.maxAttempts = maxAttempts
	}
	if lockDuration > 0 {
		e.lockDuration = lockDuration
	}
}

// SetIPThrottle installs the cross-protocol per-address login throttle.
func (e *Engine) SetIPThrottle(t *IPThrottle) {
	e.ipThrottle = t
}

// IPThrottle returns the shared per-address login throttle (may be nil).
func (e *Engine) IPThrottle() *IPThrottle {
	if e == nil {
		return nil
	}
	return e.ipThrottle
}

// SetRequireSpecialChar enforces auth.require_special_char for new passwords.
func (e *Engine) SetRequireSpecialChar(require bool) {
	e.policyMu.Lock()
	defer e.policyMu.Unlock()
	e.requireSpecial = require
}

func (e *Engine) SetMinPasswordLength(length int) {
	e.policyMu.Lock()
	defer e.policyMu.Unlock()
	if length < 0 {
		length = 0
	}
	e.minPasswordLength = length
}

func (e *Engine) CreateUser(username, password, homeDir string, admin bool) (*User, error) {
	if err := e.validatePassword(password); err != nil {
		return nil, err
	}
	normalizedHomeDir, err := NormalizeHomeDir(homeDir)
	if err != nil {
		return nil, err
	}
	hash, err := HashPassword(password, e.hashAlgo)
	if err != nil {
		return nil, err
	}
	u := &User{
		Username:     username,
		PasswordHash: hash,
		AuthProvider: AuthProviderLocal,
		HomeDir:      normalizedHomeDir,
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
	if err := e.validatePassword(password); err != nil {
		return err
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

func (e *Engine) RecordSuccessfulLogin(userID string) error {
	if e == nil || e.repo == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	return e.repo.UpdateLastLogin(userID)
}

func (e *Engine) RecordFailedLogin(userID string) error {
	if e == nil || e.repo == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	user, err := e.repo.GetByID(userID)
	if err != nil || user == nil {
		return err
	}
	return e.registerFailedLogin(user)
}

func (e *Engine) validatePassword(password string) error {
	if e == nil {
		return nil
	}
	e.policyMu.RLock()
	defer e.policyMu.RUnlock()
	if e.minPasswordLength > 0 && len(password) < e.minPasswordLength {
		return fmt.Errorf("password must be at least %d characters", e.minPasswordLength)
	}
	if e.requireSpecial && !strings.ContainsFunc(password, isSpecialPasswordRune) {
		return errors.New("password must contain at least one special character")
	}
	return nil
}

// ValidatePassword checks password against the configured policy.
func (e *Engine) ValidatePassword(password string) error {
	return e.validatePassword(password)
}

func isSpecialPasswordRune(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsSpace(r)
}

// authorizedKeyMatches compares a stored authorized_keys entry with the key a
// client proved possession of. Entries are parsed rather than string-compared
// so the usual "type base64 comment" form (and leading options) match.
func authorizedKeyMatches(entry string, key ssh.PublicKey) bool {
	parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(entry)))
	if err != nil {
		return false
	}
	return bytes.Equal(parsed.Marshal(), key.Marshal())
}

func (e *Engine) authenticateLDAPUser(ctx context.Context, shadow *User, username, password string) (*User, error) {
	if shadow != nil {
		if err := e.ensureLoginAllowed(shadow); err != nil {
			return nil, err
		}
	}
	if e.ldap == nil {
		return nil, ErrInvalidCredentials
	}

	identity, err := e.ldap.Authenticate(ctx, username, password)
	if err != nil {
		_ = e.registerFailedLogin(shadow)
		return nil, err
	}
	return e.syncLDAPUser(identity, shadow)
}

func (e *Engine) syncLDAPUser(identity *LDAPIdentity, shadow *User) (*User, error) {
	if shadow == nil {
		shadow = &User{
			Username:     identity.Username,
			AuthProvider: AuthProviderLDAP,
			Email:        identity.Email,
			Type:         identity.Type,
			HomeDir:      identity.HomeDir,
			Enabled:      true,
			Permissions:  identity.Permissions,
			PrimaryGroup: firstGroup(identity.Groups),
			SecondaryGrps: func() []string {
				if len(identity.Groups) <= 1 {
					return nil
				}
				out := make([]string, 0, len(identity.Groups)-1)
				out = append(out, identity.Groups[1:]...)
				return out
			}(),
		}
		if err := e.repo.Create(shadow); err != nil {
			return nil, err
		}
		return e.repo.GetByUsername(identity.Username)
	}

	shadow.AuthProvider = AuthProviderLDAP
	shadow.Email = identity.Email
	shadow.Type = identity.Type
	if strings.TrimSpace(shadow.HomeDir) == "" {
		shadow.HomeDir = identity.HomeDir
	}
	shadow.PrimaryGroup = firstGroup(identity.Groups)
	if len(identity.Groups) > 1 {
		shadow.SecondaryGrps = append(shadow.SecondaryGrps[:0], identity.Groups[1:]...)
	} else {
		shadow.SecondaryGrps = nil
	}
	if err := e.repo.Update(shadow); err != nil {
		return nil, err
	}
	return shadow, nil
}

func (e *Engine) ensureLoginAllowed(user *User) error {
	if user == nil {
		return nil
	}
	if !user.Enabled {
		return ErrUserDisabled
	}
	now := time.Now().UTC()
	if user.LockedUntil != nil && user.LockedUntil.After(now) {
		return ErrUserLocked
	}
	return nil
}

func (e *Engine) registerFailedLogin(user *User) error {
	if user == nil {
		return nil
	}
	e.policyMu.RLock()
	lockout, maxAttempts, lockDuration := !e.lockoutDisabled, e.maxAttempts, e.lockDuration
	e.policyMu.RUnlock()
	user.FailedLogins++
	if lockout && user.FailedLogins >= maxAttempts {
		until := time.Now().UTC().Add(lockDuration)
		user.LockedUntil = &until
	}
	return e.repo.Update(user)
}

func isLDAPUser(user *User) bool {
	if user == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(user.AuthProvider), AuthProviderLDAP)
}

func firstGroup(groups []string) string {
	if len(groups) == 0 {
		return ""
	}
	return groups[0]
}
