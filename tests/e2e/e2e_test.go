//go:build integration

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/analytics"
	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/skill"
	gosync "github.com/skael-dev/skael/internal/sync"
	"github.com/skael-dev/skael/internal/testutil"
)

const testAPIKey = "e2e-test-api-key"

// startTestServer spins up a fully-wired HTTP server backed by a real Postgres
// instance (via testcontainers). It returns the server URL and a cleanup
// function that must be called when the test finishes.
func startTestServer(t *testing.T) (serverURL string, cleanup func()) {
	t.Helper()

	// 1. Provision an ephemeral Postgres and run all migrations.
	pool := testutil.SetupTestDB(t)

	// 2. Create storage in a temp dir.
	storageDir := t.TempDir()
	storage, err := platform.NewLocalStorage(storageDir)
	require.NoError(t, err)

	// 3. Create chi router with auth middleware (mirrors main.go exactly).
	router := chi.NewMux()
	router.Use(middleware.Recoverer)
	router.Use(middleware.RealIP)
	router.Use(auth.Middleware(nil, nil, nil, testAPIKey))

	// Enforce body size limit before Huma buffers the request body.
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10 MB
			next.ServeHTTP(w, r)
		})
	})

	// 4. Create Huma API.
	humaConfig := huma.DefaultConfig("Skael API", "1.0.0")
	api := humachi.New(router, humaConfig)

	// 5. Register health endpoint (auth skips /api/health).
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/api/health",
	}, func(ctx context.Context, input *struct{}) (*struct {
		Body struct {
			Status string `json:"status"`
		}
	}, error) {
		out := &struct {
			Body struct {
				Status string `json:"status"`
			}
		}{}
		out.Body.Status = "ok"
		return out, nil
	})

	// 6. Register skill routes.
	skillStore := skill.NewStore(pool)
	skill.RegisterRoutes(api, router, skillStore, storage)

	// 7. Register sync manifest route.
	syncStore := gosync.NewStore(pool)
	huma.Register(api, huma.Operation{
		OperationID: "get-manifest",
		Method:      http.MethodGet,
		Path:        "/api/sync/manifest",
	}, func(ctx context.Context, input *struct{}) (*struct {
		Body []gosync.ManifestEntry
	}, error) {
		entries, err := syncStore.GetManifest(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("", err)
		}
		return &struct {
			Body []gosync.ManifestEntry
		}{Body: entries}, nil
	})

	// 8. Register analytics routes.
	analyticsStore := analytics.NewStore(pool)
	analytics.RegisterRoutes(api, analyticsStore)

	// 9. Start server on a random OS-assigned port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := &http.Server{
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	go server.Serve(listener) //nolint:errcheck

	// 10. Build the URL and return a cleanup that shuts the server down.
	url := "http://" + listener.Addr().String()
	cleanupFn := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}

	// Wait briefly for the server to accept connections.
	require.Eventually(t, func() bool {
		resp, err := http.Get(url + "/api/health")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 100*time.Millisecond, "server did not become ready in time")

	return url, cleanupFn
}

// packTestdataDir packs the testdata directory at the given relative path into
// an in-memory tar.gz archive and returns the bytes.
func packTestdataDir(t *testing.T, relDir string) []byte {
	t.Helper()
	// testdata is next to this file in the e2e package directory.
	dir := filepath.Join("testdata", relDir)
	archiveBytes, _, _, err := skill.Pack(dir)
	require.NoError(t, err, "packing %s", dir)
	return archiveBytes
}

// postEvent posts a single activation event directly to the test server without
// using the typed client (so we keep the test concise).
func postEvent(t *testing.T, serverURL, apiKey, skillName, agent string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{
		"skill_name":     skillName,
		"agent":          agent,
		"trigger_type":   "manual",
		"project_hash":   "proj-hash",
		"developer_hash": "dev-" + agent,
	})
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/events", bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode, "post event failed")
}

// ---------------------------------------------------------------------------
// Scenario 1: Publish a skill and retrieve it via the API.
// ---------------------------------------------------------------------------

func TestE2E_PublishAndRetrieve(t *testing.T) {
	serverURL, cleanup := startTestServer(t)
	defer cleanup()

	c := client.New(serverURL, testAPIKey)

	// Create the skill record.
	created, err := c.CreateSkill("e2e-test-skill", "initial description")
	require.NoError(t, err)
	require.Equal(t, "e2e-test-skill", created.Name)

	// Publish version 1 using the clean testdata archive.
	archiveBytes := packTestdataDir(t, "clean-skill")
	ver, _, err := c.PublishVersion("e2e-test-skill", archiveBytes)
	require.NoError(t, err)
	require.NotNil(t, ver)
	require.Equal(t, 1, ver.Version)

	// Retrieve the skill — latest_version must be 1 and the description
	// should be updated from the SKILL.md frontmatter.
	sk, err := c.GetSkill("e2e-test-skill")
	require.NoError(t, err)
	require.NotNil(t, sk)
	require.Equal(t, 1, sk.LatestVersion)
	require.Equal(t, "A clean skill for end-to-end testing", sk.Description)

	// Download the archive and verify it is non-empty.
	archiveData, err := c.DownloadVersion("e2e-test-skill", 1)
	require.NoError(t, err)
	require.NotEmpty(t, archiveData)
}

// ---------------------------------------------------------------------------
// Scenario 2: Sync flow — publish → manifest → download → verify content.
// ---------------------------------------------------------------------------

func TestE2E_SyncFlow(t *testing.T) {
	serverURL, cleanup := startTestServer(t)
	defer cleanup()

	c := client.New(serverURL, testAPIKey)

	// Publish a skill so the manifest has at least one entry.
	_, err := c.CreateSkill("e2e-test-skill", "sync test")
	require.NoError(t, err)

	archiveBytes := packTestdataDir(t, "clean-skill")
	_, _, err = c.PublishVersion("e2e-test-skill", archiveBytes)
	require.NoError(t, err)

	// Fetch the manifest.
	manifest, err := c.GetManifest()
	require.NoError(t, err)
	require.Len(t, manifest, 1, "manifest should have exactly 1 entry")
	require.Equal(t, "e2e-test-skill", manifest[0].Name)
	require.Equal(t, 1, manifest[0].Version)
	require.NotEmpty(t, manifest[0].Checksum)

	// Download version 1 and unpack to a temp dir.
	downloaded, err := c.DownloadVersion("e2e-test-skill", 1)
	require.NoError(t, err)
	require.NotEmpty(t, downloaded)

	unpackDir := t.TempDir()
	err = skill.Unpack(bytes.NewReader(downloaded), unpackDir)
	require.NoError(t, err)

	// Verify SKILL.md is present in the unpacked dir.
	skillMDPath := filepath.Join(unpackDir, "SKILL.md")
	data, err := os.ReadFile(skillMDPath)
	require.NoError(t, err)
	require.Contains(t, string(data), "E2E Test Skill")
}

// ---------------------------------------------------------------------------
// Scenario 3: Security scan blocks publish of a skill with secrets.
// ---------------------------------------------------------------------------

func TestE2E_SecurityScanBlocks(t *testing.T) {
	serverURL, cleanup := startTestServer(t)
	defer cleanup()

	c := client.New(serverURL, testAPIKey)

	// Create the bad-skill record.
	_, err := c.CreateSkill("bad-skill", "should be blocked")
	require.NoError(t, err)

	// Pack the bad testdata directory that contains a secret.
	archiveBytes := packTestdataDir(t, "bad-skill")

	// PublishVersion should fail with a 422 Unprocessable Entity.
	ver, scanBody, err := c.PublishVersion("bad-skill", archiveBytes)
	require.Error(t, err, "expected publish to be rejected by security scan")
	require.Nil(t, ver)

	// The error must be a 422 (critical security scan rejection).
	apiErr, ok := err.(*client.APIError)
	require.True(t, ok, "expected *client.APIError, got %T: %v", err, err)
	require.Equal(t, http.StatusUnprocessableEntity, apiErr.StatusCode)

	// The scan body returned by the client should be non-nil.
	require.NotNil(t, scanBody, "scan body should be present in the error response")

	// Verify the scan report is valid JSON.
	var report interface{}
	require.NoError(t, json.Unmarshal(scanBody, &report), "scan body should be valid JSON")
}

// ---------------------------------------------------------------------------
// Scenario 4: Activation tracking — post events, query summary.
// ---------------------------------------------------------------------------

func TestE2E_ActivationTracking(t *testing.T) {
	serverURL, cleanup := startTestServer(t)
	defer cleanup()

	c := client.New(serverURL, testAPIKey)

	// Create and publish a skill (needed so the name is registered, though the
	// analytics endpoint does not require the skill to exist in the skills table).
	_, err := c.CreateSkill("tracked-skill", "activation tracking test")
	require.NoError(t, err)
	archiveBytes := packTestdataDir(t, "clean-skill")
	_, _, err = c.PublishVersion("tracked-skill", archiveBytes)
	require.NoError(t, err)

	// Post 2 events from 2 distinct developers/agents.
	postEvent(t, serverURL, testAPIKey, "tracked-skill", "claude")
	postEvent(t, serverURL, testAPIKey, "tracked-skill", "codex")

	// Retrieve activation summary.
	req, err := http.NewRequest(http.MethodGet,
		serverURL+"/api/skills/tracked-skill/activations?days=30", nil)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", testAPIKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var summary analytics.ActivationSummary
	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(bodyBytes, &summary))

	require.Equal(t, 2, summary.TotalCount)
	// Each event has a distinct developer_hash ("dev-claude" vs "dev-codex").
	require.Equal(t, 2, summary.UniqueDevs)
	// Both agents should appear in the breakdown.
	require.Contains(t, summary.ByAgent, "claude")
	require.Contains(t, summary.ByAgent, "codex")
	require.Equal(t, 1, summary.ByAgent["claude"])
	require.Equal(t, 1, summary.ByAgent["codex"])
}

// ---------------------------------------------------------------------------
// Scenario 5: Config round-trip — write and read config + state files.
// ---------------------------------------------------------------------------

func TestE2E_ConfigRoundTrip(t *testing.T) {
	// This scenario is purely filesystem-based — no server needed.
	dir := t.TempDir()

	// --- Config ---
	cfg := &config.Config{
		Endpoint: "https://api.skael.dev",
		APIKey:   "sk-test-key-abc123",
	}
	require.NoError(t, config.WriteConfig(dir, cfg))

	readCfg, err := config.ReadConfig(dir)
	require.NoError(t, err)
	require.Equal(t, cfg.Endpoint, readCfg.Endpoint)
	require.Equal(t, cfg.APIKey, readCfg.APIKey)

	// --- State ---
	state := &config.SyncState{
		LastSync: "2024-01-15T10:00:00Z",
		Skills: []config.SyncedSkill{
			{Name: "my-skill", Version: 3, Checksum: "abc123def456"},
			{Name: "other-skill", Version: 1, Checksum: "deadbeef1234"},
		},
	}
	require.NoError(t, config.WriteState(dir, state))

	readState, err := config.ReadState(dir)
	require.NoError(t, err)
	require.Equal(t, state.LastSync, readState.LastSync)
	require.Len(t, readState.Skills, 2)
	require.Equal(t, "my-skill", readState.Skills[0].Name)
	require.Equal(t, 3, readState.Skills[0].Version)
	require.Equal(t, "abc123def456", readState.Skills[0].Checksum)
	require.Equal(t, "other-skill", readState.Skills[1].Name)
	require.Equal(t, 1, readState.Skills[1].Version)

	// Missing state file returns empty state, not an error.
	emptyDir := t.TempDir()
	emptyState, err := config.ReadState(emptyDir)
	require.NoError(t, err)
	require.NotNil(t, emptyState)
	require.Empty(t, emptyState.Skills)
}

// ---------------------------------------------------------------------------
// Scenario 6: Full CLI lifecycle — onboarding flow end-to-end.
// ---------------------------------------------------------------------------

func TestE2E_FullLifecycle(t *testing.T) {
	// 1. Start test server.
	serverURL, cleanup := startTestServer(t)
	defer cleanup()

	c := client.New(serverURL, testAPIKey)

	// 2. Create and publish a skill.
	skillName := "lifecycle-skill"
	_, err := c.CreateSkill(skillName, "full lifecycle test skill")
	require.NoError(t, err)

	archiveBytes := packTestdataDir(t, "clean-skill")
	ver, _, err := c.PublishVersion(skillName, archiveBytes)
	require.NoError(t, err)
	require.NotNil(t, ver)
	require.Equal(t, 1, ver.Version)

	// 3. Configure — simulate setup by writing config to a temp dir.
	configDir := t.TempDir()
	cfg := &config.Config{
		Endpoint: serverURL,
		APIKey:   testAPIKey,
	}
	require.NoError(t, config.WriteConfig(configDir, cfg))

	readCfg, err := config.ReadConfig(configDir)
	require.NoError(t, err)
	require.Equal(t, serverURL, readCfg.Endpoint)
	require.Equal(t, testAPIKey, readCfg.APIKey)

	// 4. Get manifest via client, verify the skill appears.
	manifest, err := c.GetManifest()
	require.NoError(t, err)
	require.Len(t, manifest, 1)
	require.Equal(t, skillName, manifest[0].Name)
	require.Equal(t, 1, manifest[0].Version)
	require.NotEmpty(t, manifest[0].Checksum)
	manifestChecksum := manifest[0].Checksum

	// 5. Download the archive, verify checksum matches manifest entry.
	downloaded, err := c.DownloadVersion(skillName, 1)
	require.NoError(t, err)
	require.NotEmpty(t, downloaded)

	sum := sha256.Sum256(downloaded)
	downloadedChecksum := fmt.Sprintf("%x", sum)
	require.Equal(t, manifestChecksum, downloadedChecksum,
		"downloaded archive checksum must match manifest entry")

	// 6. Extract to a simulated agent directory.
	agentDir := t.TempDir()
	err = skill.Unpack(bytes.NewReader(downloaded), agentDir)
	require.NoError(t, err)

	// 7. Verify SKILL.md exists in the extracted location.
	skillMDPath := filepath.Join(agentDir, "SKILL.md")
	data, err := os.ReadFile(skillMDPath)
	require.NoError(t, err)
	require.Contains(t, string(data), "E2E Test Skill")

	// 8. Write sync state file, read it back, verify.
	state := &config.SyncState{
		LastSync: "2026-01-01T00:00:00Z",
		Skills: []config.SyncedSkill{
			{Name: skillName, Version: 1, Checksum: manifestChecksum},
		},
	}
	require.NoError(t, config.WriteState(configDir, state))

	readState, err := config.ReadState(configDir)
	require.NoError(t, err)
	require.Equal(t, state.LastSync, readState.LastSync)
	require.Len(t, readState.Skills, 1)
	require.Equal(t, skillName, readState.Skills[0].Name)
	require.Equal(t, 1, readState.Skills[0].Version)
	require.Equal(t, manifestChecksum, readState.Skills[0].Checksum)

	// 9. Post an activation event, query activations, verify count.
	postEvent(t, serverURL, testAPIKey, skillName, "claude")

	req, err := http.NewRequest(http.MethodGet,
		serverURL+"/api/skills/"+skillName+"/activations?days=30", nil)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", testAPIKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var summary analytics.ActivationSummary
	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(bodyBytes, &summary))

	require.Equal(t, 1, summary.TotalCount)
	require.Contains(t, summary.ByAgent, "claude")
	require.Equal(t, 1, summary.ByAgent["claude"])
}
