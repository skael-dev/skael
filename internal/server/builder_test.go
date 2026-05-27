package server_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/server"
	"github.com/stretchr/testify/require"
)

func TestBuilder_DefaultCapabilities(t *testing.T) {
	b := server.NewBuilder(nil, nil)
	caps := b.Capabilities()
	resp := caps.Response()

	require.Equal(t, "oss", resp.Edition)
	require.False(t, resp.Features.RBAC)
	require.False(t, resp.Features.SSO)
	require.False(t, resp.Features.Audit)
}

func TestBuilder_WithAuthorizer_EnablesFeatures(t *testing.T) {
	b := server.NewBuilder(nil, nil)
	b.WithAuthorizer(server.AllowAll{})
	caps := b.Capabilities()
	resp := caps.Response()

	require.True(t, resp.Features.RBAC)
	require.True(t, resp.Features.Teams)
}

func TestBuilder_WithAuditLog_EnablesFeature(t *testing.T) {
	b := server.NewBuilder(nil, nil)
	b.WithAuditLog(server.NoopAuditLogger{})
	caps := b.Capabilities()
	resp := caps.Response()

	require.True(t, resp.Features.Audit)
}
