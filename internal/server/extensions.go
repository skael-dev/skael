package server

import (
	"context"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/skill"
)

type Authorizer interface {
	Authorize(ctx context.Context, user *auth.User, action, resource string) error
}

type AuditEvent struct {
	UserID   string
	Action   string
	Resource string
	Detail   string
}

type AuditLogger interface {
	Log(ctx context.Context, event AuditEvent) error
}

type IdentityProvider interface {
	Authenticate(ctx context.Context, token string) (*auth.User, error)
	MetadataURL() string
}

type PolicyEnforcer interface {
	CheckPublish(ctx context.Context, sk *skill.Skill) error
	CheckActivation(ctx context.Context, skillName, agent string) error
}

type ScanRuleProvider interface {
	ExtraRules(ctx context.Context) ([]scan.Rule, error)
}
