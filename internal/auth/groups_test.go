package auth

import (
	"errors"
	"testing"

	"github.com/kervanserver/kervan/internal/store"
)

func newGroupTestRepos(t *testing.T) (*UserRepository, *GroupRepository) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	users := NewUserRepository(st)
	return users, NewGroupRepository(st, users)
}

func TestGroupCRUDRenameAndDelete(t *testing.T) {
	users, groups := newGroupTestRepos(t)
	readOnly := UserPermissions{Download: true, ListDir: true}
	g := &Group{Name: "Auditors", Permissions: readOnly, MaxStorage: 1 << 20}
	if err := groups.Create(g); err != nil {
		t.Fatal(err)
	}
	if err := groups.Create(&Group{Name: "auditors"}); err == nil {
		t.Fatal("case-insensitive duplicate accepted")
	}
	for _, bad := range []string{"", "bad name", "../x", "-lead"} {
		if err := groups.Create(&Group{Name: bad}); err == nil {
			t.Errorf("invalid name %q accepted", bad)
		}
	}
	if err := groups.Create(&Group{Name: "q", MaxStorage: -2}); err == nil {
		t.Error("max_storage -2 accepted")
	}

	u := &User{Username: "ann", PrimaryGroup: "auditors", SecondaryGrps: []string{"AUDITORS", "other"}}
	if err := users.Create(u); err != nil {
		t.Fatal(err)
	}

	// Rename rewrites memberships.
	g.Name = "reviewers"
	if err := groups.Update(g); err != nil {
		t.Fatal(err)
	}
	if found, _ := groups.GetByName("auditors"); found != nil {
		t.Fatal("old name still resolves")
	}
	got, _ := users.GetByUsername("ann")
	if got.PrimaryGroup != "reviewers" || got.SecondaryGrps[0] != "reviewers" || got.SecondaryGrps[1] != "other" {
		t.Fatalf("memberships not renamed: %q %v", got.PrimaryGroup, got.SecondaryGrps)
	}

	// Delete refuses while members remain, unless forced.
	if err := groups.Delete(g.ID, false); !errors.Is(err, ErrGroupInUse) {
		t.Fatalf("delete with members: %v", err)
	}
	if err := groups.Delete(g.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _ = users.GetByUsername("ann")
	if got.PrimaryGroup != "" || len(got.SecondaryGrps) != 1 || got.SecondaryGrps[0] != "other" {
		t.Fatalf("memberships not removed: %q %v", got.PrimaryGroup, got.SecondaryGrps)
	}
	if err := groups.Delete(g.ID, false); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestResolvePolicy(t *testing.T) {
	own := UserPermissions{Upload: true, Download: true}
	tmpl := UserPermissions{Download: true, ListDir: true}
	group := &Group{Name: "g", Permissions: tmpl, MaxStorage: 500}

	cases := []struct {
		name      string
		user      User
		group     *Group
		wantPerms UserPermissions
		wantQuota int64
	}{
		{"no group uses own perms and default quota", User{Permissions: own}, nil, own, 1000},
		{"group template and quota", User{Permissions: own}, group, tmpl, 500},
		{"custom permissions override group", User{Permissions: own, CustomPermissions: true}, group, own, 500},
		{"user quota overrides group", User{Permissions: own, MaxStorage: 42}, group, tmpl, 42},
		{"user unlimited", User{Permissions: own, MaxStorage: -1}, group, tmpl, 0},
		{"group unlimited", User{Permissions: own}, &Group{Permissions: tmpl, MaxStorage: -1}, tmpl, 0},
		{"group inherits default quota", User{Permissions: own}, &Group{Permissions: tmpl}, tmpl, 1000},
	}
	for _, tc := range cases {
		p := ResolvePolicy(&tc.user, tc.group, PolicyDefaults{MaxStorage: 1000})
		if p.Permissions.Upload != tc.wantPerms.Upload || p.Permissions.ListDir != tc.wantPerms.ListDir {
			t.Errorf("%s: permissions %+v, want %+v", tc.name, p.Permissions, tc.wantPerms)
		}
		if p.MaxStorage != tc.wantQuota {
			t.Errorf("%s: quota %d, want %d", tc.name, p.MaxStorage, tc.wantQuota)
		}
	}
}

func TestPolicyForIgnoresUnknownGroup(t *testing.T) {
	_, groups := newGroupTestRepos(t)
	own := UserPermissions{Upload: true}
	// LDAP users carry raw directory group names that may not exist here.
	p, err := groups.PolicyFor(&User{Permissions: own, PrimaryGroup: "cn=staff,dc=example"}, PolicyDefaults{})
	if err != nil || !p.Permissions.Upload || p.Group != nil {
		t.Fatalf("unknown group must fall back to own permissions: %+v %v", p, err)
	}
}

func TestResolvePolicyBandwidth(t *testing.T) {
	g := &Group{MaxBandwidth: 500}
	d := PolicyDefaults{MaxBandwidth: 100}
	for _, tc := range []struct {
		user  User
		group *Group
		want  int64
	}{
		{User{}, nil, 100},
		{User{}, g, 500},
		{User{MaxBandwidth: 42}, g, 42},
		{User{MaxBandwidth: -1}, g, 0},
		{User{}, &Group{MaxBandwidth: -1}, 0},
		{User{}, &Group{}, 100},
	} {
		if got := ResolvePolicy(&tc.user, tc.group, d).MaxBandwidth; got != tc.want {
			t.Errorf("user=%d group=%v -> %d, want %d", tc.user.MaxBandwidth, tc.group, got, tc.want)
		}
	}
}
