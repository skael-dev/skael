//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/ownership"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/quality"
	"github.com/skael-dev/skael/internal/skill"
	gosync "github.com/skael-dev/skael/internal/sync"
	"github.com/skael-dev/skael/internal/testutil"
)

// ---------------------------------------------------------------------------
// ownershipEnv: the same server wiring as evalEnv (see eval_queue_test.go —
// the harness the publish-gate scenarios in publish_gate_test.go already
// use), with the ownership store/resolver added exactly as
// internal/server/routes.go wires it: ownership.NewStore, ownership.NewResolver
// passed as skill.RouteOptions.Ownership, ownership.RegisterRoutes, and the
// same resolver handed to skill.RegisterReviewQueueRoutes. Multiple
// authenticated actors (admin, an owner, a non-owner) are needed for these
// scenarios, so this harness keeps a pool of users/API keys rather than the
// single instance-owner key evalEnv uses.
// ---------------------------------------------------------------------------

type actor struct {
	id     string
	email  string
	apiKey string
}

type ownershipEnv struct {
	serverURL string
	userStore *auth.UserStore
	keyStore  *auth.KeyStore
	admin     actor // instance owner: the first account, fully privileged
}

// startOwnershipServer spins up a fully-wired HTTP server — skills, ownership,
// eval suites/queue, quality, and the sync manifest — backed by a real
// Postgres instance (via testcontainers). This mirrors startServerWithFloor
// in eval_queue_test.go, plus the ownership wiring internal/server/routes.go
// does between "Skills" and "Ownership rules".
func startOwnershipServer(t *testing.T) *ownershipEnv {
	t.Helper()

	pool := testutil.SetupTestDB(t)

	storageDir := t.TempDir()
	storage, err := platform.NewLocalStorage(storageDir)
	require.NoError(t, err)

	userStore := auth.NewUserStore(pool)
	keyStore := auth.NewKeyStore(pool)

	sessionManager := scs.New()

	router := chi.NewMux()
	router.Use(middleware.Recoverer)
	router.Use(platform.ClientIP(platform.ParseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))))
	router.Use(sessionManager.LoadAndSave)
	router.Use(auth.Middleware(sessionManager, userStore, keyStore))

	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10 MB
			next.ServeHTTP(w, r)
		})
	})

	humaConfig := huma.DefaultConfig("Skael API", "1.0.0")
	api := humachi.New(router, humaConfig)

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

	skillStore := skill.NewStore(pool)
	evalPool := evalqueue.NewPool(pool)
	qualityStore := quality.NewStore(pool)
	suiteRegistry := evalsuite.NewRegistry(pool, storage)
	ownershipStore := ownership.NewStore(pool)
	ownerResolver := ownership.NewResolver(ownershipStore, userStore)

	skill.RegisterRoutes(api, router, skillStore, storage, skill.RouteOptions{
		Queue:     evalQueueAdapter{q: evalPool},
		Suites:    evalSuiteAdapter{r: suiteRegistry},
		Ownership: ownerResolver,
	})

	// Ownership rules — same call site as internal/server/routes.go, right
	// after skill.RegisterRoutes.
	ownership.RegisterRoutes(api, ownershipStore, userStore)

	// Cross-skill review queue, wired with the real resolver so the
	// per-reason review route's owner check actually has something to ask.
	skill.RegisterReviewQueueRoutes(api, skillStore, ownerResolver)

	evalsuite.RegisterRoutes(api, router, suiteRegistry, skillStore)
	evalqueue.RegisterRoutes(api, evalPool, qualityStore, skillStore, suiteRegistry, evalqueue.RouteOptions{
		Releaser: skill.NewReleaser(skillStore),
	})
	quality.RegisterRoutes(api, qualityStore, skillStore)

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

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := &http.Server{
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	go server.Serve(listener) //nolint:errcheck

	url := "http://" + listener.Addr().String()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})

	require.Eventually(t, func() bool {
		resp, err := http.Get(url + "/api/health")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 30*time.Second, 100*time.Millisecond, "server did not become ready in time")

	env := &ownershipEnv{serverURL: url, userStore: userStore, keyStore: keyStore}
	env.admin = env.createUser(t, auth.RoleOwner, "owner@test.local")
	return env
}

// createUser registers a user with the given role and issues them a personal
// API key, mirroring how startServerWithFloor sets up its single instance
// owner in eval_queue_test.go.
func (e *ownershipEnv) createUser(t *testing.T, role, email string) actor {
	t.Helper()
	pwHash, err := auth.HashPassword("test-password")
	require.NoError(t, err)
	row, err := e.userStore.CreateWithRole(context.Background(), email, email, pwHash, role)
	require.NoError(t, err)

	fullKey, prefix, err := auth.GenerateAPIKey()
	require.NoError(t, err)
	keyHash, err := auth.HashAPIKey(fullKey)
	require.NoError(t, err)
	_, err = e.keyStore.Create(context.Background(), row.ID, email+"-key", prefix, keyHash)
	require.NoError(t, err)

	return actor{id: row.ID, email: email, apiKey: fullKey}
}

// doAs issues an authenticated request as who and returns the raw response.
// Mirrors evalEnv.do in publish_gate_test.go, parameterised over the actor.
func (e *ownershipEnv) doAs(t *testing.T, who actor, method, path, contentType string, body []byte) rawResponse {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, e.serverURL+path, rdr)
	require.NoError(t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("X-API-Key", who.apiKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return rawResponse{Code: resp.StatusCode, Body: string(b)}
}

func (e *ownershipEnv) getJSONAs(t *testing.T, who actor, path string, out interface{}) rawResponse {
	t.Helper()
	resp := e.doAs(t, who, http.MethodGet, path, "", nil)
	if resp.Code == http.StatusOK && out != nil {
		require.NoError(t, json.Unmarshal([]byte(resp.Body), out))
	}
	return resp
}

// createSkillAs registers a skill so a version can be published under it.
func (e *ownershipEnv) createSkillAs(t *testing.T, who actor, name string) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"name":        name,
		"description": "ownership e2e test skill",
	})
	require.NoError(t, err)
	resp := e.doAs(t, who, http.MethodPost, "/api/skills", "application/json", payload)
	require.Equal(t, http.StatusCreated, resp.Code, "create skill body: %s", resp.Body)
}

// createRule creates (or replaces the members of) an ownership rule as who,
// returning the rule's id.
func (e *ownershipEnv) createRule(t *testing.T, who actor, pattern string, members []string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]interface{}{
		"pattern": pattern,
		"members": members,
	})
	require.NoError(t, err)
	resp := e.doAs(t, who, http.MethodPost, "/api/ownership/rules", "application/json", payload)
	require.Equal(t, http.StatusOK, resp.Code, "create rule body: %s", resp.Body)
	var out struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Body), &out))
	return out.ID
}

func (e *ownershipEnv) deleteRule(t *testing.T, who actor, id string) rawResponse {
	t.Helper()
	return e.doAs(t, who, http.MethodDelete, "/api/ownership/rules/"+id, "", nil)
}

// ownershipGateResult is the publish response, seen through the gate's eyes,
// extended with HoldReasons — gateResult (publish_gate_test.go) does not
// carry it, and the two-reason scenario needs to assert on the set directly
// rather than infer it from Reasons.
type ownershipGateResult struct {
	Version  int  `json:"version"`
	Created  bool `json:"created"`
	Decision struct {
		Outcome     string   `json:"outcome"`
		HoldReasons []string `json:"hold_reasons"`
		Reasons     []struct {
			Class    string `json:"class"`
			Severity string `json:"severity"`
			Clears   string `json:"clears"`
		} `json:"reasons"`
	} `json:"decision"`
}

// publishAs posts archive as a new version of skillName, as who, and returns
// the decoded gate verdict. Requires 201.
func (e *ownershipEnv) publishAs(t *testing.T, who actor, skillName string, archive []byte) ownershipGateResult {
	t.Helper()
	resp := e.doAs(t, who, http.MethodPost, "/api/skills/"+skillName+"/versions", "application/gzip", archive)
	require.Equal(t, http.StatusCreated, resp.Code, "publish body: %s", resp.Body)
	var out ownershipGateResult
	require.NoError(t, json.Unmarshal([]byte(resp.Body), &out))
	return out
}

// reviewAs posts a human approve/reject decision on a held version as who,
// optionally naming which outstanding reason it clears.
func (e *ownershipEnv) reviewAs(t *testing.T, who actor, skillName string, version int, action, reason, holdReason string) rawResponse {
	t.Helper()
	body := map[string]string{"action": action, "reason": reason}
	if holdReason != "" {
		body["hold_reason"] = holdReason
	}
	payload, err := json.Marshal(body)
	require.NoError(t, err)
	return e.doAs(t, who, http.MethodPost,
		"/api/skills/"+skillName+"/versions/"+strconv.Itoa(version)+"/review", "application/json", payload)
}

func (e *ownershipEnv) manifestNames(t *testing.T, who actor) []string {
	t.Helper()
	var entries []struct {
		Name string `json:"name"`
	}
	e.getJSONAs(t, who, "/api/sync/manifest", &entries)
	names := make([]string, 0, len(entries))
	for _, en := range entries {
		names = append(names, en.Name)
	}
	return names
}

func (e *ownershipEnv) downloadVersionAs(t *testing.T, who actor, skillName string, version int) rawResponse {
	t.Helper()
	return e.doAs(t, who, http.MethodGet,
		"/api/skills/"+skillName+"/versions/"+strconv.Itoa(version)+"/download", "", nil)
}

func (e *ownershipEnv) getVersionAs(t *testing.T, who actor, skillName string, version int) skill.Version {
	t.Helper()
	var body struct {
		Versions []skill.Version `json:"versions"`
	}
	e.getJSONAs(t, who, "/api/skills/"+skillName+"/versions", &body)
	for _, v := range body.Versions {
		if v.Version == version {
			return v
		}
	}
	t.Fatalf("%s v%d not found among %d versions", skillName, version, len(body.Versions))
	return skill.Version{}
}

// ---------------------------------------------------------------------------
// Fixtures.
// ---------------------------------------------------------------------------

// cleanBundle is an ordinary skill bundle that trips no scan rule at all — the
// fixture for scenarios where ownership is the only thing under test.
func cleanBundle(t *testing.T, name string) []byte {
	t.Helper()
	dir := t.TempDir()
	md := "---\n" +
		"name: " + name + "\n" +
		"description: A perfectly ordinary skill with nothing to flag.\n" +
		"---\n\n" +
		"# " + name + "\n\n" +
		"Does an ordinary, unremarkable thing.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644))
	archive, _, _, err := skill.Pack(dir)
	require.NoError(t, err)
	return archive
}

// ---------------------------------------------------------------------------
// Scenarios.
// ---------------------------------------------------------------------------

// TestNonOwnerProposalIsReviewedAndReleased is the whole feature, over HTTP,
// through the real server: a rule is created, a non-owner's publish is held,
// the non-owner cannot approve their own version, and the actual owner's
// approval releases it onto every latest-resolving surface.
func TestNonOwnerProposalIsReviewedAndReleased(t *testing.T) {
	srv := startOwnershipServer(t)

	alice := srv.createUser(t, auth.RoleMember, "alice@test.local")
	carol := srv.createUser(t, auth.RoleMember, "carol@test.local")

	srv.createSkillAs(t, srv.admin, "payments:refunds")

	// 1. admin creates rule payments:* -> alice
	srv.createRule(t, srv.admin, "payments:*", []string{alice.id})

	// 2. carol (member, non-owner) publishes payments:refunds -> 201, held.
	pub := srv.publishAs(t, carol, "payments:refunds", cleanBundle(t, "payments:refunds"))
	require.Equal(t, "needs_review", pub.Decision.Outcome)

	// 3. GET /api/sync/manifest does NOT contain payments:refunds.
	require.NotContains(t, srv.manifestNames(t, carol), "payments:refunds")

	// 4. carol approves her own version -> 403.
	got := srv.reviewAs(t, carol, "payments:refunds", pub.Version, "approve", "looks fine to me", "ownership")
	require.Equal(t, http.StatusForbidden, got.Code, "body: %s", got.Body)

	// 5. alice approves with hold_reason=ownership -> 200, released.
	got = srv.reviewAs(t, alice, "payments:refunds", pub.Version, "approve", "reviewed, looks fine", "ownership")
	require.Equal(t, http.StatusOK, got.Code, "body: %s", got.Body)

	// 6. manifest now contains it, and download serves the archive.
	require.Contains(t, srv.manifestNames(t, carol), "payments:refunds")
	require.Equal(t, http.StatusOK, srv.downloadVersionAs(t, carol, "payments:refunds", pub.Version).Code)
	require.Equal(t, "released", srv.getVersionAs(t, srv.admin, "payments:refunds", pub.Version).GateState)
}

// TestScanAndOwnershipClearIndependently is the two-reason case: a scan
// finding and an ownership hold are two different questions on the same
// version, and clearing one must never be mistaken for clearing both — on
// any surface (the review response, the version's own gate_state, or the
// manifest).
func TestScanAndOwnershipClearIndependently(t *testing.T) {
	srv := startOwnershipServer(t)

	alice := srv.createUser(t, auth.RoleMember, "alice@test.local")
	carol := srv.createUser(t, auth.RoleMember, "carol@test.local")

	srv.createSkillAs(t, srv.admin, "payments:deploy")
	srv.createRule(t, srv.admin, "payments:*", []string{alice.id})

	// 1. carol publishes a bundle with an appealable finding to an owned skill.
	pub := srv.publishAs(t, carol, "payments:deploy", deployBundle(t))
	require.Equal(t, "needs_review", pub.Decision.Outcome)

	// 2. gate_state needs_review, hold_reasons == [scan ownership].
	require.Equal(t, []string{"scan", "ownership"}, pub.Decision.HoldReasons)
	require.Equal(t, "needs_review", srv.getVersionAs(t, srv.admin, "payments:deploy", pub.Version).GateState)

	// 3. alice (owner, member) approves ownership -> still needs_review. Every
	// surface must agree: the review response itself, and a fresh read of the
	// version.
	got := srv.reviewAs(t, alice, "payments:deploy", pub.Version, "approve", "ownership looks fine", "ownership")
	require.Equal(t, http.StatusOK, got.Code, "body: %s", got.Body)
	var afterOwnership skill.Version
	require.NoError(t, json.Unmarshal([]byte(got.Body), &afterOwnership))
	require.Equal(t, "needs_review", afterOwnership.GateState,
		"clearing ownership alone must not release a version still held for a scan finding")
	require.Equal(t, "needs_review", srv.getVersionAs(t, srv.admin, "payments:deploy", pub.Version).GateState)
	require.NotContains(t, srv.manifestNames(t, carol), "payments:deploy")

	// 4. alice tries to approve scan -> 403: a scan finding is an
	// instance-level decision, not a namespace owner's to waive.
	got = srv.reviewAs(t, alice, "payments:deploy", pub.Version, "approve", "trust me", "scan")
	require.Equal(t, http.StatusForbidden, got.Code, "body: %s", got.Body)
	require.Equal(t, "needs_review", srv.getVersionAs(t, srv.admin, "payments:deploy", pub.Version).GateState)

	// 5. admin approves scan -> released.
	got = srv.reviewAs(t, srv.admin, "payments:deploy", pub.Version, "approve", "reviewed the finding", "scan")
	require.Equal(t, http.StatusOK, got.Code, "body: %s", got.Body)
	require.Equal(t, "released", srv.getVersionAs(t, srv.admin, "payments:deploy", pub.Version).GateState)
	require.Contains(t, srv.manifestNames(t, carol), "payments:deploy")
}

// TestUnownedSkillPublishesDirectly is O5, and the reason the upgrade is
// safe: an install with no ownership rules at all behaves exactly as it did
// before this feature shipped — a clean publish releases directly, with no
// hold and no review step.
func TestUnownedSkillPublishesDirectly(t *testing.T) {
	srv := startOwnershipServer(t)

	carol := srv.createUser(t, auth.RoleMember, "carol@test.local")

	// No rules at all anywhere on this instance.
	srv.createSkillAs(t, srv.admin, "docs:writer")

	pub := srv.publishAs(t, carol, "docs:writer", cleanBundle(t, "docs:writer"))
	require.Equal(t, "allow", pub.Decision.Outcome)
	require.Empty(t, pub.Decision.HoldReasons)
	require.Equal(t, "released", srv.getVersionAs(t, srv.admin, "docs:writer", pub.Version).GateState)
	require.Contains(t, srv.manifestNames(t, carol), "docs:writer")
}

// TestDeletingARuleDoesNotUnpublishAnything is O10: an ownership rule governs
// who may propose a new version, not what has already been served. Deleting
// it after a version is released must not touch the manifest or the
// download for that version.
func TestDeletingARuleDoesNotUnpublishAnything(t *testing.T) {
	srv := startOwnershipServer(t)

	alice := srv.createUser(t, auth.RoleMember, "alice@test.local")
	carol := srv.createUser(t, auth.RoleMember, "carol@test.local")

	srv.createSkillAs(t, srv.admin, "billing:invoices")
	ruleID := srv.createRule(t, srv.admin, "billing:*", []string{alice.id})

	pub := srv.publishAs(t, carol, "billing:invoices", cleanBundle(t, "billing:invoices"))
	require.Equal(t, "needs_review", pub.Decision.Outcome)

	got := srv.reviewAs(t, alice, "billing:invoices", pub.Version, "approve", "reviewed, looks fine", "ownership")
	require.Equal(t, http.StatusOK, got.Code, "body: %s", got.Body)
	require.Equal(t, "released", srv.getVersionAs(t, srv.admin, "billing:invoices", pub.Version).GateState)

	beforeNames := srv.manifestNames(t, carol)
	require.Contains(t, beforeNames, "billing:invoices")
	beforeDownload := srv.downloadVersionAs(t, carol, "billing:invoices", pub.Version)
	require.Equal(t, http.StatusOK, beforeDownload.Code)

	// Delete the rule that governed this skill.
	del := srv.deleteRule(t, srv.admin, ruleID)
	require.Equal(t, http.StatusOK, del.Code, "body: %s", del.Body)

	// Nothing already served is disturbed.
	afterNames := srv.manifestNames(t, carol)
	require.Contains(t, afterNames, "billing:invoices")
	afterDownload := srv.downloadVersionAs(t, carol, "billing:invoices", pub.Version)
	require.Equal(t, http.StatusOK, afterDownload.Code)
	require.Equal(t, beforeDownload.Body, afterDownload.Body)
	require.Equal(t, "released", srv.getVersionAs(t, srv.admin, "billing:invoices", pub.Version).GateState)
}
