package auth

// Role names. An instance has exactly one owner (the first account created),
// any number of admins the owner promotes, and members — the default for every
// new account.
const (
	// RoleOwner is the first account created on an instance. It has full
	// control and is the only role that can change other people's roles.
	RoleOwner = "owner"
	// RoleAdmin is granted by the owner. Everything operational, including
	// overriding a blocked publish.
	RoleAdmin = "admin"
	// RoleMember is the default for every new account: publish, sync, browse.
	RoleMember = "member"
)

// IsOwner reports whether the user is the instance owner. A nil user is never
// the owner.
func (u *User) IsOwner() bool {
	return u != nil && u.Role == RoleOwner
}

// IsPrivileged reports whether the user may perform privileged operations —
// currently the owner and any admin. This is the single definition of
// privilege: gate on it rather than comparing role strings at call sites. A nil
// user is never privileged.
func (u *User) IsPrivileged() bool {
	return u != nil && (u.Role == RoleOwner || u.Role == RoleAdmin)
}
