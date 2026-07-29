package skill

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skael-dev/skael/internal/auth"
)

func TestPublishOverrideAllowed(t *testing.T) {
	admin := auth.ContextWithUser(context.Background(), &auth.User{ID: "1", Email: "a@example.com", Role: "admin"})
	member := auth.ContextWithUser(context.Background(), &auth.User{ID: "2", Email: "m@example.com", Role: "member"})
	anon := context.Background()

	assert.True(t, publishOverrideAllowed(admin, true), "an admin who asks may override")
	assert.False(t, publishOverrideAllowed(admin, false), "an override must be requested explicitly")
	assert.False(t, publishOverrideAllowed(member, true), "a non-admin may not override")
	assert.False(t, publishOverrideAllowed(anon, true), "an unauthenticated caller may not override")
}
