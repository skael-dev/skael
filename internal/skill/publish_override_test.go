package skill

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skael-dev/skael/internal/auth"
)

func TestPublishOverrideAllowed(t *testing.T) {
	owner := auth.ContextWithUser(context.Background(), &auth.User{ID: "1", Email: "o@example.com", Role: auth.RoleOwner})
	admin := auth.ContextWithUser(context.Background(), &auth.User{ID: "2", Email: "a@example.com", Role: auth.RoleAdmin})
	member := auth.ContextWithUser(context.Background(), &auth.User{ID: "3", Email: "m@example.com", Role: auth.RoleMember})
	unknownRole := auth.ContextWithUser(context.Background(), &auth.User{ID: "4", Email: "u@example.com", Role: "superuser"})
	anon := context.Background()

	assert.True(t, publishOverrideAllowed(owner, true), "the owner who asks may override")
	assert.True(t, publishOverrideAllowed(admin, true), "an admin who asks may override")
	assert.False(t, publishOverrideAllowed(owner, false), "an override must be requested explicitly")
	assert.False(t, publishOverrideAllowed(admin, false), "an override must be requested explicitly")
	assert.False(t, publishOverrideAllowed(member, true), "a member may not override")
	assert.False(t, publishOverrideAllowed(unknownRole, true), "an unrecognised role may not override")
	assert.False(t, publishOverrideAllowed(anon, true), "an unauthenticated caller may not override")
}
