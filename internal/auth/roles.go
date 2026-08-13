package auth

const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// IsOwner reports whether the user is the instance owner.
func (u *User) IsOwner() bool {
	return u != nil && u.Role == RoleOwner
}

// IsPrivileged reports whether the user may perform privileged operations.
// Gate on this rather than comparing role strings at call sites.
func (u *User) IsPrivileged() bool {
	return u != nil && (u.Role == RoleOwner || u.Role == RoleAdmin)
}
