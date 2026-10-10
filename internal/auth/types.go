package auth

import "time"

type UserType string

const (
	UserTypeAdmin   UserType = "admin"
	UserTypeVirtual UserType = "virtual"
)

type User struct {
	ID             string          `json:"id" yaml:"id"`
	Username       string          `json:"username" yaml:"username"`
	PasswordHash   string          `json:"password_hash" yaml:"password_hash"`
	AuthProvider   string          `json:"auth_provider,omitempty" yaml:"auth_provider,omitempty"`
	TOTPSecret     string          `json:"totp_secret,omitempty" yaml:"totp_secret,omitempty"`
	TOTPEnabled    bool            `json:"totp_enabled,omitempty" yaml:"totp_enabled,omitempty"`
	AuthorizedKeys []string        `json:"authorized_keys,omitempty" yaml:"authorized_keys,omitempty"`
	Email          string          `json:"email,omitempty" yaml:"email,omitempty"`
	Type           UserType        `json:"type" yaml:"type"`
	HomeDir        string          `json:"home_dir" yaml:"home_dir"`
	Permissions    UserPermissions `json:"permissions" yaml:"permissions"`
	Enabled        bool            `json:"enabled" yaml:"enabled"`
	CreatedAt      time.Time       `json:"created_at" yaml:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at" yaml:"updated_at"`
	LastLoginAt    *time.Time      `json:"last_login_at,omitempty" yaml:"last_login_at,omitempty"`
	FailedLogins   int             `json:"failed_logins" yaml:"failed_logins"`
	LockedUntil    *time.Time      `json:"locked_until,omitempty" yaml:"locked_until,omitempty"`
	PrimaryGroup   string          `json:"primary_group,omitempty" yaml:"primary_group,omitempty"`
	SecondaryGrps  []string        `json:"secondary_groups,omitempty" yaml:"secondary_groups,omitempty"`
	// CustomPermissions makes Permissions authoritative even when the
	// primary group defines a permission template.
	CustomPermissions bool `json:"custom_permissions,omitempty" yaml:"custom_permissions,omitempty"`
	// MaxStorage is the user's storage quota in bytes: 0 inherits from the
	// primary group (then quota.default_max_storage), -1 is unlimited.
	MaxStorage int64 `json:"max_storage,omitempty" yaml:"max_storage,omitempty"`
	// MaxBandwidth caps the user's combined transfer rate in bytes/s: 0
	// inherits from the primary group (then bandwidth.default_user_rate),
	// -1 is unlimited.
	MaxBandwidth int64 `json:"max_bandwidth,omitempty" yaml:"max_bandwidth,omitempty"`
	// MaxFiles caps the number of regular files: 0 inherits from the primary
	// group (then quota.default_max_files), -1 is unlimited.
	MaxFiles int64 `json:"max_files,omitempty" yaml:"max_files,omitempty"`
	// ExternalID is the identity provider's stable subject for externally
	// authenticated accounts (OIDC "sub").
	ExternalID string `json:"external_id,omitempty" yaml:"external_id,omitempty"`
	// SessionsValidAfter invalidates WebUI/API sessions issued before it
	// (set on password changes).
	SessionsValidAfter *time.Time `json:"sessions_valid_after,omitempty" yaml:"sessions_valid_after,omitempty"`
}

// Group is a permission and quota template shared by its members. Users
// reference groups by name through PrimaryGroup (which supplies the
// template) and SecondaryGrps (membership only).
type Group struct {
	ID          string          `json:"id" yaml:"id"`
	Name        string          `json:"name" yaml:"name"`
	Description string          `json:"description,omitempty" yaml:"description,omitempty"`
	Permissions UserPermissions `json:"permissions" yaml:"permissions"`
	// MaxStorage is the members' default quota in bytes: 0 falls back to
	// quota.default_max_storage, -1 is unlimited.
	MaxStorage int64 `json:"max_storage,omitempty" yaml:"max_storage,omitempty"`
	// MaxBandwidth is the members' default rate limit in bytes/s: 0 falls
	// back to bandwidth.default_user_rate, -1 is unlimited.
	MaxBandwidth int64 `json:"max_bandwidth,omitempty" yaml:"max_bandwidth,omitempty"`
	// MaxFiles is the members' default file-count quota: 0 falls back to
	// quota.default_max_files, -1 is unlimited.
	MaxFiles  int64     `json:"max_files,omitempty" yaml:"max_files,omitempty"`
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`
	UpdatedAt time.Time `json:"updated_at" yaml:"updated_at"`
}

type UserPermissions struct {
	Upload      bool     `json:"upload" yaml:"upload"`
	Download    bool     `json:"download" yaml:"download"`
	Delete      bool     `json:"delete" yaml:"delete"`
	Rename      bool     `json:"rename" yaml:"rename"`
	CreateDir   bool     `json:"create_dir" yaml:"create_dir"`
	ListDir     bool     `json:"list_dir" yaml:"list_dir"`
	Chmod       bool     `json:"chmod" yaml:"chmod"`
	MaxFileSize int64    `json:"max_file_size,omitempty" yaml:"max_file_size,omitempty"`
	AllowedExt  []string `json:"allowed_ext,omitempty" yaml:"allowed_ext,omitempty"`
	DeniedExt   []string `json:"denied_ext,omitempty" yaml:"denied_ext,omitempty"`
}

func DefaultUserPermissions() UserPermissions {
	return UserPermissions{
		Upload:    true,
		Download:  true,
		Delete:    true,
		Rename:    true,
		CreateDir: true,
		ListDir:   true,
		Chmod:     false,
	}
}
