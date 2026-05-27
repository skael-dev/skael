package server_test

import (
	"context"
	"testing"

	"github.com/skael-dev/skael/internal/server"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/stretchr/testify/require"
)

func TestAllowAll_Authorize(t *testing.T) {
	a := server.AllowAll{}
	err := a.Authorize(context.Background(), nil, "write", "skills")
	require.NoError(t, err)
}

func TestNoopAuditLogger_Log(t *testing.T) {
	l := server.NoopAuditLogger{}
	err := l.Log(context.Background(), server.AuditEvent{Action: "create"})
	require.NoError(t, err)
}

func TestAllowAllPolicy_CheckPublish(t *testing.T) {
	p := server.AllowAllPolicy{}
	err := p.CheckPublish(context.Background(), &skill.Skill{Name: "test"})
	require.NoError(t, err)
}

func TestAllowAllPolicy_CheckActivation(t *testing.T) {
	p := server.AllowAllPolicy{}
	err := p.CheckActivation(context.Background(), "test", "claude")
	require.NoError(t, err)
}
