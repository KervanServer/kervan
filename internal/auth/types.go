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
