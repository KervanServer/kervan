package auth

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/util/ulid"
)

const (
	collGroups      = "groups"
	collGroupByName = "groups_idx_name"
)

var (
	ErrGroupNotFound = errors.New("group not found")
	ErrGroupInUse    = errors.New("group has members")

	groupNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// ValidateGroupName enforces portable group names (they appear in config
// mappings and URLs).
func ValidateGroupName(name string) error {
	if !groupNamePattern.MatchString(name) {
		return errors.New("group name must be 1-64 characters of letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	return nil
}

func validateMaxStorage(v int64) error {
	if v < -1 {
		return errors.New("max_storage must be -1 (unlimited), 0 (inherit) or a byte count")
	}
	return nil
}

type GroupRepository struct {
	store *store.Store
	users *UserRepository
}

func NewGroupRepository(s *store.Store, users *UserRepository) *GroupRepository {
	return &GroupRepository{store: s, users: users}
}

func groupIndexKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func (r *GroupRepository) Create(g *Group) error {
	g.Name = strings.TrimSpace(g.Name)
	if err := ValidateGroupName(g.Name); err != nil {
		return err
	}
	if err := validateMaxStorage(g.MaxStorage); err != nil {
		return err
	}
	if g.MaxBandwidth < -1 {
		return errors.New("max_bandwidth must be -1 (unlimited), 0 (inherit) or bytes per second")
	}
	if existing, err := r.GetByName(g.Name); err != nil {
		return err
	} else if existing != nil {
		return fmt.Errorf("group %q already exists", g.Name)
	}
	if g.ID == "" {
		g.ID = ulid.New()
	}
	now := time.Now().UTC()
	g.CreatedAt, g.UpdatedAt = now, now
	if err := r.store.Put(collGroups, g.ID, g); err != nil {
		return err
	}
	return r.store.Put(collGroupByName, groupIndexKey(g.Name), g.ID)
}

func (r *GroupRepository) GetByID(id string) (*Group, error) {
	var g Group
	if err := r.store.Get(collGroups, id, &g); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &g, nil
}

// GetByName looks a group up case-insensitively; nil when it does not exist.
func (r *GroupRepository) GetByName(name string) (*Group, error) {
	if r == nil || strings.TrimSpace(name) == "" {
		return nil, nil
	}
	var id string
	if err := r.store.Get(collGroupByName, groupIndexKey(name), &id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return r.GetByID(id)
}

func (r *GroupRepository) List() ([]*Group, error) {
	var groups []*Group
	if err := r.store.List(collGroups, &groups); err != nil {
		return nil, err
	}
	slices.SortFunc(groups, func(a, b *Group) int { return strings.Compare(groupIndexKey(a.Name), groupIndexKey(b.Name)) })
	return groups, nil
}

// Update saves g. A rename also rewrites every member's group references so
// memberships survive it.
func (r *GroupRepository) Update(g *Group) error {
	existing, err := r.GetByID(g.ID)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrGroupNotFound
	}
	g.Name = strings.TrimSpace(g.Name)
	if err := ValidateGroupName(g.Name); err != nil {
		return err
	}
	if err := validateMaxStorage(g.MaxStorage); err != nil {
		return err
	}
	if g.MaxBandwidth < -1 {
		return errors.New("max_bandwidth must be -1 (unlimited), 0 (inherit) or bytes per second")
	}
	renamed := groupIndexKey(existing.Name) != groupIndexKey(g.Name)
	if renamed {
		if other, err := r.GetByName(g.Name); err != nil {
			return err
		} else if other != nil && other.ID != g.ID {
			return fmt.Errorf("group %q already exists", g.Name)
		}
	}
	g.CreatedAt = existing.CreatedAt
	g.UpdatedAt = time.Now().UTC()
	if err := r.store.Put(collGroups, g.ID, g); err != nil {
		return err
	}
	if err := r.store.Put(collGroupByName, groupIndexKey(g.Name), g.ID); err != nil {
		return err
	}
	if !renamed && existing.Name == g.Name {
		return nil
	}
	if renamed {
		_ = r.store.Delete(collGroupByName, groupIndexKey(existing.Name))
	}
	return r.rewriteMemberships(existing.Name, g.Name)
}

// Delete removes a group. A group that still has members is refused with
// ErrGroupInUse unless force is set, in which case the memberships are
// dropped and members fall back to their own permissions.
func (r *GroupRepository) Delete(id string, force bool) error {
	g, err := r.GetByID(id)
	if err != nil {
		return err
	}
	if g == nil {
		return ErrGroupNotFound
	}
	members, err := r.Members(g.Name)
	if err != nil {
		return err
	}
	if len(members) > 0 && !force {
		return fmt.Errorf("%w: %d user(s) reference %q", ErrGroupInUse, len(members), g.Name)
	}
	if err := r.rewriteMemberships(g.Name, ""); err != nil {
		return err
	}
	_ = r.store.Delete(collGroupByName, groupIndexKey(g.Name))
	return r.store.Delete(collGroups, id)
}

// Members lists users whose primary or secondary groups include name.
func (r *GroupRepository) Members(name string) ([]*User, error) {
	if r.users == nil {
		return nil, nil
	}
	users, err := r.users.List()
	if err != nil {
		return nil, err
	}
	var out []*User
	for _, u := range users {
		if u != nil && userInGroup(u, name) {
			out = append(out, u)
		}
	}
	return out, nil
}

func userInGroup(u *User, name string) bool {
	key := groupIndexKey(name)
	if groupIndexKey(u.PrimaryGroup) == key {
		return true
	}
	for _, g := range u.SecondaryGrps {
		if groupIndexKey(g) == key {
			return true
		}
	}
	return false
}

// rewriteMemberships renames (or, with to == "", removes) a group reference
// on every member.
func (r *GroupRepository) rewriteMemberships(from, to string) error {
	members, err := r.Members(from)
	if err != nil {
		return err
	}
	key := groupIndexKey(from)
	for _, u := range members {
		if groupIndexKey(u.PrimaryGroup) == key {
			u.PrimaryGroup = to
		}
		secondary := u.SecondaryGrps[:0]
		for _, g := range u.SecondaryGrps {
			switch {
			case groupIndexKey(g) != key:
				secondary = append(secondary, g)
			case to != "":
				secondary = append(secondary, to)
			}
		}
		u.SecondaryGrps = secondary
		if err := r.users.Update(u); err != nil {
			return err
		}
	}
	return nil
}

// PolicyDefaults are the server-wide fallbacks for users and groups that
// do not set a limit.
type PolicyDefaults struct {
	MaxStorage   int64
	MaxBandwidth int64
}

// Policy is a user's effective permissions and limits after group
// inheritance.
type Policy struct {
	Permissions UserPermissions
	// MaxStorage is the resolved quota in bytes; 0 means unlimited.
	MaxStorage int64
	// MaxBandwidth is the resolved rate limit in bytes/s; 0 means unlimited.
	MaxBandwidth int64
	// Group is the primary group that supplied the template, if any.
	Group *Group
}

// ResolvePolicy applies group inheritance. The primary group, when it
// exists, supplies permissions unless the user has CustomPermissions, and
// supplies the quota unless the user sets MaxStorage. A primary group that
// does not exist (e.g. an LDAP group with no Kervan counterpart) is ignored.
func ResolvePolicy(u *User, primary *Group, defaults PolicyDefaults) Policy {
	p := Policy{Permissions: u.Permissions, Group: primary}
	if primary != nil && !u.CustomPermissions {
		p.Permissions = primary.Permissions
	}
	resolve := func(own, group, fallback int64) int64 {
		v := own
		if v == 0 && primary != nil {
			v = group
		}
		if v == 0 {
			v = fallback
		}
		return max(v, 0) // -1 (unlimited) becomes 0
	}
	var groupStorage, groupBandwidth int64
	if primary != nil {
		groupStorage, groupBandwidth = primary.MaxStorage, primary.MaxBandwidth
	}
	p.MaxStorage = resolve(u.MaxStorage, groupStorage, defaults.MaxStorage)
	p.MaxBandwidth = resolve(u.MaxBandwidth, groupBandwidth, defaults.MaxBandwidth)
	return p
}

// PolicyFor resolves u's policy, looking its primary group up in r.
func (r *GroupRepository) PolicyFor(u *User, defaults PolicyDefaults) (Policy, error) {
	primary, err := r.GetByName(u.PrimaryGroup)
	if err != nil {
		return Policy{}, err
	}
	return ResolvePolicy(u, primary, defaults), nil
}
