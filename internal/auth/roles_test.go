package auth_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skael-dev/skael/internal/auth"
)

func TestUserRoles(t *testing.T) {
	tests := []struct {
		name        string
		user        *auth.User
		owner       bool
		privileged  bool
		description string
	}{
		{name: "owner", user: &auth.User{Role: auth.RoleOwner}, owner: true, privileged: true},
		{name: "admin", user: &auth.User{Role: auth.RoleAdmin}, owner: false, privileged: true},
		{name: "member", user: &auth.User{Role: auth.RoleMember}, owner: false, privileged: false},
		{name: "empty role", user: &auth.User{}, owner: false, privileged: false},
		{name: "unknown role", user: &auth.User{Role: "superuser"}, owner: false, privileged: false},
		{name: "nil user", user: nil, owner: false, privileged: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Must not panic on a nil receiver.
			assert.Equal(t, tc.owner, tc.user.IsOwner(), "IsOwner")
			assert.Equal(t, tc.privileged, tc.user.IsPrivileged(), "IsPrivileged")
		})
	}
}

func TestRoleConstants(t *testing.T) {
	// The constants are persisted in the database and checked by a CHECK
	// constraint; changing a value is a migration, not a rename.
	assert.Equal(t, "owner", auth.RoleOwner)
	assert.Equal(t, "admin", auth.RoleAdmin)
	assert.Equal(t, "member", auth.RoleMember)
}
