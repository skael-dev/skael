package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/server"
)

func TestCapabilities_OSS(t *testing.T) {
	router := chi.NewMux()
	api := humachi.New(router, huma.DefaultConfig("test", "1.0.0"))

	caps := server.NewCapabilities()
	caps.Register(api)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/capabilities", nil)
	router.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var body server.CapabilitiesResponse
	err := json.NewDecoder(w.Body).Decode(&body)
	require.NoError(t, err)
	require.Equal(t, "oss", body.Edition)
	require.False(t, body.Features.Teams)
	require.False(t, body.Features.RBAC)
	require.False(t, body.Features.SSO)
	require.False(t, body.Features.Audit)
	require.False(t, body.Features.Governance)
	require.False(t, body.Features.CustomScan)
	require.False(t, body.Features.AdvancedAnalytics)
	require.Nil(t, body.License)
}

func TestCapabilities_WithFeatures(t *testing.T) {
	caps := server.NewCapabilities()
	caps.EnableFeature("rbac")
	caps.EnableFeature("teams")
	caps.EnableFeature("audit")
	caps.SetEdition("enterprise")

	resp := caps.Response()
	require.Equal(t, "enterprise", resp.Edition)
	require.True(t, resp.Features.RBAC)
	require.True(t, resp.Features.Teams)
	require.True(t, resp.Features.Audit)
	require.False(t, resp.Features.SSO)
}

// Ensure context import is used (required by capabilities.go Register signature)
var _ = context.Background
