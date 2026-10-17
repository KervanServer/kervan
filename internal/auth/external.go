package auth

import (
	"errors"
	"fmt"
	"strings"
)

// AuthProviderOIDC marks accounts that sign in through an OpenID provider.
const AuthProviderOIDC = "oidc"

var (
	// ErrAccountConflict means an external identity claims a username that
	// belongs to a different account (a local/LDAP user, or another subject).
	ErrAccountConflict = errors.New("username belongs to another account")
	// ErrNotProvisioned means auto-creation is off and no account exists.
	ErrNotProvisioned = errors.New("account is not provisioned")
)

// ExternalIdentity is a verified identity from an external provider.
type ExternalIdentity struct {
	Provider string
	Subject  string
	Username string
	Email    string
	// Admin is nil when the provider does not decide the role (no admin
	// groups configured): new users become regular users and existing users
	// keep their role.
	Admin           *bool
	PrimaryGroup    string
	SecondaryGroups []string
	// SyncGroups replaces the account's group memberships on every login.
	SyncGroups bool
	HomeDir    string
}

// unusablePasswordHash can never verify, so external accounts cannot log in
// with a password over FTP/SFTP or the password API.
const unusablePasswordHash = "!external"

// ProvisionExternalUser finds or creates the account for id and syncs its
// attributes. An existing account is only reused when it belongs to the same
// provider and subject, so a provider cannot take over a local account by
// asserting its username.
func (e *Engine) ProvisionExternalUser(id ExternalIdentity, autoCreate bool) (*User, error) {
	id.Username = strings.TrimSpace(id.Username)
	if id.Username == "" || id.Subject == "" {
		return nil, errors.New("external identity needs a username and a subject")
	}
	user, err := e.repo.GetByUsername(id.Username)
	if err != nil {
		return nil, err
	}
	if user == nil {
		if !autoCreate {
			return nil, ErrNotProvisioned
		}
		homeDir, err := NormalizeHomeDir(id.HomeDir)
		if err != nil {
			return nil, fmt.Errorf("home directory: %w", err)
		}
		user = &User{
			Username:      id.Username,
			PasswordHash:  unusablePasswordHash,
			AuthProvider:  id.Provider,
			ExternalID:    id.Subject,
			Email:         id.Email,
			Type:          UserTypeVirtual,
			HomeDir:       homeDir,
			Enabled:       true,
			Permissions:   DefaultUserPermissions(),
			PrimaryGroup:  id.PrimaryGroup,
			SecondaryGrps: id.SecondaryGroups,
		}
		if id.Admin != nil && *id.Admin {
			user.Type = UserTypeAdmin
		}
		if err := e.repo.Create(user); err != nil {
			return nil, err
		}
		return user, nil
	}

	// Check and sync against the latest stored record in one transaction, so
	// a disable or local edit saved since the lookup is neither bypassed nor
	// overwritten.
	saved, err := e.repo.Mutate(user.ID, func(u *User) error {
		if !strings.EqualFold(u.AuthProvider, id.Provider) {
			return ErrAccountConflict
		}
		if u.ExternalID != "" && u.ExternalID != id.Subject {
			return ErrAccountConflict
		}
		if !u.Enabled {
			return ErrUserDisabled
		}
		u.ExternalID = id.Subject
		if id.Email != "" {
			u.Email = id.Email
		}
		if id.Admin != nil {
			if *id.Admin {
				u.Type = UserTypeAdmin
			} else {
				u.Type = UserTypeVirtual
			}
		}
		if id.SyncGroups {
			u.PrimaryGroup = id.PrimaryGroup
			u.SecondaryGrps = append([]string(nil), id.SecondaryGroups...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if saved == nil {
		return nil, ErrNotProvisioned
	}
	return saved, nil
}
