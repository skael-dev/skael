package server

import (
	"context"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/skill"
)

type AllowAll struct{}

func (AllowAll) Authorize(_ context.Context, _ *auth.User, _, _ string) error {
	return nil
}

type NoopAuditLogger struct{}

func (NoopAuditLogger) Log(_ context.Context, _ AuditEvent) error {
	return nil
}

type AllowAllPolicy struct{}

func (AllowAllPolicy) CheckPublish(_ context.Context, _ *skill.Skill) error {
	return nil
}

func (AllowAllPolicy) CheckActivation(_ context.Context, _, _ string) error {
	return nil
}
