//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/worker"
)

// ---------------------------------------------------------------------------
// Fixtures.
//
// Each fixture asserts what the scanner actually produces from it, not what
// its name suggests. A fixture that quietly stops matching a rule turns every
// test built on it into theatre — the publish succeeds, the gate never
// engages, and the assertions all pass for the wrong reason.
// ---------------------------------------------------------------------------

// deployBundle is an ordinary deploy skill that trips one high-severity
// execution finding (`eval "$VAR"` in a fenced shell block). Execution is an
// appealable class, so this bundle is the phase's headline case: held at
// publish, releasable by a verified score or an admin.
func deployBundle(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	md := "---\n" +
		"name: deployer\n" +
		"description: Deploys the service to the cluster.\n" +
		"---\n\n" +
		"# deployer\n\n" +
		"Run the release step:\n\n" +
		"```bash\n" +
		"eval \"$DEPLOY_COMMAND\"\n" +
		"```\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644))

	assertScanFindings(t, dir, "high", "execution")

	archive, _, _, err := skill.Pack(dir)
	require.NoError(t, err)
	return archive
}

// secretBundle ships a credential. Secret is an unappealable class, so this
// bundle must be rejected outright — no version row, no override, no score.
func secretBundle(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	md := "---\n" +
		"name: leaky\n" +
		"description: Ships a credential in the bundle.\n" +
		"---\n\n" +
		"# leaky\n\n" +
		"Configure the client with sk-proj-abc123def456ghi789jklmnop before running.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644))

	assertScanFindings(t, dir, "critical", "secret")

	archive, _, _, err := skill.Pack(dir)
	require.NoError(t, err)
	return archive
}

// assertScanFindings runs the real scanner over dir and asserts that it
// produced at least one finding and that *every* finding carries the given
// severity and class. Requiring every finding to match — rather than merely
// one — is what keeps a fixture honest: a bundle that also trips an
// unappealable rule would change which branch of the gate it exercises
// without changing the test's name.
//
// Note that findings are not deduplicated across the line-pair pass, so a
// single offending line legitimately yields two identical findings; the count
// is deliberately not pinned.
func assertScanFindings(t *testing.T, dir, severity, class string) {
	t.Helper()
	rep, err := scan.ScanDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, rep.Findings, "fixture no longer trips any scan rule")
	for _, f := range rep.Findings {
		assert.Equal(t, severity, f.Severity,
			"fixture finding %s changed severity: %+v", f.Rule, f)
		assert.Equal(t, class, string(f.Class),
			"fixture finding %s changed class: %+v", f.Rule, f)
	}
}

// ---------------------------------------------------------------------------
// Gate helpers on evalEnv (the harness in eval_queue_test.go).
// ---------------------------------------------------------------------------

// gateResult is the publish response, seen through the gate's eyes.
type gateResult struct {
	Version  int  `json:"version"`
	Created  bool `json:"created"`
	Decision struct {
		Outcome string `json:"outcome"`
		Reasons []struct {
			Class    string `json:"class"`
			Severity string `json:"severity"`
			Clears   string `json:"clears"`
		} `json:"reasons"`
	} `json:"decision"`
	Quality struct {
		State string `json:"state"`
		JobID string `json:"job_id"`
	} `json:"quality"`
}

// rawResponse is a status code plus the body, for the paths where the status
// is the assertion.
type rawResponse struct {
	Code int
	Body string
}

// do issues an authenticated request and returns the raw response.
func (e *evalEnv) do(t *testing.T, method, path, contentType string, body []byte) rawResponse {
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
	req.Header.Set("X-API-Key", e.apiKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return rawResponse{Code: resp.StatusCode, Body: string(b)}
}

// publishRaw posts archive as a new version of skillName, appending query
// verbatim (e.g. "?override=true"), and returns the raw response.
func (e *evalEnv) publishRaw(t *testing.T, skillName string, archive []byte, query string) rawResponse {
	t.Helper()
	return e.do(t, http.MethodPost,
		"/api/skills/"+skillName+"/versions"+query, "application/gzip", archive)
}

// publishGated publishes and requires a 201, returning the gate's verdict.
func (e *evalEnv) publishGated(t *testing.T, skillName string, archive []byte) gateResult {
	t.Helper()
	resp := e.publishRaw(t, skillName, archive, "")
	require.Equal(t, http.StatusCreated, resp.Code, "publish body: %s", resp.Body)
	var out gateResult
	require.NoError(t, json.Unmarshal([]byte(resp.Body), &out))
	return out
}

// listSkillNames returns the names GET /api/skills reports.
func (e *evalEnv) listSkillNames(t *testing.T) []string {
	t.Helper()
	var body struct {
		Skills []skill.Skill `json:"skills"`
	}
	e.getJSON(t, "/api/skills?limit=100", &body)
	names := make([]string, 0, len(body.Skills))
	for _, s := range body.Skills {
		names = append(names, s.Name)
	}
	return names
}

// searchNames returns the names GET /api/search reports for q.
func (e *evalEnv) searchNames(t *testing.T, q string) []string {
	t.Helper()
	var body struct {
		Skills []skill.Skill `json:"skills"`
	}
	e.getJSON(t, "/api/search?q="+q, &body)
	names := make([]string, 0, len(body.Skills))
	for _, s := range body.Skills {
		names = append(names, s.Name)
	}
	return names
}

// manifestNames returns the skill names in the sync manifest.
func (e *evalEnv) manifestNames(t *testing.T) []string {
	t.Helper()
	var entries []struct {
		Name string `json:"name"`
	}
	e.getJSON(t, "/api/sync/manifest", &entries)
	names := make([]string, 0, len(entries))
	for _, en := range entries {
		names = append(names, en.Name)
	}
	return names
}

// latestVersion returns skills.latest_version as the detail endpoint reports
// it. Zero means nothing is being served: there is no "download latest"
// route, so every latest-resolving client resolves through this field and a
// zero here is what makes the held archive unreachable.
func (e *evalEnv) latestVersion(t *testing.T, skillName string) int {
	t.Helper()
	var sk skill.Skill
	e.getJSON(t, "/api/skills/"+skillName, &sk)
	return sk.LatestVersion
}

// downloadVersion fetches a specific version's archive.
func (e *evalEnv) downloadVersion(t *testing.T, skillName string, version int) rawResponse {
	t.Helper()
	return e.do(t, http.MethodGet,
		"/api/skills/"+skillName+"/versions/"+strconv.Itoa(version)+"/download", "", nil)
}

// getVersion returns one version row.
func (e *evalEnv) getVersion(t *testing.T, skillName string, version int) skill.Version {
	t.Helper()
	var body struct {
		Versions []skill.Version `json:"versions"`
	}
	e.getJSON(t, "/api/skills/"+skillName+"/versions", &body)
	for _, v := range body.Versions {
		if v.Version == version {
			return v
		}
	}
	t.Fatalf("%s v%d not found among %d versions", skillName, version, len(body.Versions))
	return skill.Version{}
}

// reviewQueueResult is the wire shape of GET /api/review/queue.
type reviewQueueResult struct {
	Held []struct {
		SkillName    string          `json:"skill_name"`
		Version      int             `json:"version"`
		GateState    string          `json:"gate_state"`
		GateDecision json.RawMessage `json:"gate_decision,omitempty"`
	} `json:"held"`
	Total int `json:"total"`
}

// reviewQueue returns every version held for review.
func (e *evalEnv) reviewQueue(t *testing.T) reviewQueueResult {
	t.Helper()
	var out reviewQueueResult
	e.getJSON(t, "/api/review/queue", &out)
	return out
}

// review posts a human approve/reject decision on a held version.
func (e *evalEnv) review(t *testing.T, skillName string, version int, action, reason string) rawResponse {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"action": action, "reason": reason})
	require.NoError(t, err)
	return e.do(t, http.MethodPost,
		"/api/skills/"+skillName+"/versions/"+strconv.Itoa(version)+"/review",
		"application/json", payload)
}

// ---------------------------------------------------------------------------
// gateRunner is a worker.Runner producing a report shaped the way a real one
// is for gate purposes: a complete panel and a real engine version. Ingestion
// rejects an empty or "dev" engine version outright, and an incomplete panel
// can never clear a hold however high the score — so a stub that omits either
// would make a release test pass or fail for a reason unrelated to the floor.
// ---------------------------------------------------------------------------

type gateRunner struct {
	headline      float64
	panelComplete bool
	engineVersion string
}

func (g gateRunner) Run(_ context.Context, in worker.RunInput) (*report.Report, error) {
	now := time.Now()
	engine := g.engineVersion
	if engine == "" {
		engine = "e2e-engine-1"
	}
	return &report.Report{
		SchemaVersion: report.SchemaVersion,
		Skill:         in.Skill,
		SpecVersion:   1,
		Tier:          "full",
		SuiteRef:      in.SuiteRef,
		EngineVersion: engine,
		Headline:      g.headline,
		PanelComplete: g.panelComplete,
		StartedAt:     now,
		FinishedAt:    now,
	}, nil
}

// runEval drains exactly one queued job with a runner scoring headline.
func (e *evalEnv) runEval(t *testing.T, headline float64) {
	t.Helper()
	w := e.newWorker(t, gateRunner{headline: headline, panelComplete: true})
	worked, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	require.True(t, worked, "no eval job was claimable")
}

// ---------------------------------------------------------------------------
// Scenarios.
// ---------------------------------------------------------------------------

// TestGateHeldSkillBecomesServableOnAVerifiedScore is the phase's headline
// claim: the ordinary deploy skill that trips a heuristic is publishable
// because it demonstrably behaves.
func TestGateHeldSkillBecomesServableOnAVerifiedScore(t *testing.T) {
	srv := startServerWithFloor(t, 70)

	srv.createSkill(t, "deployer")
	ref := srv.pushSuite(t, "deployer", fixtureSuiteArchive(t))

	// 1. Publish a bundle that trips a high-severity execution rule.
	pub := srv.publishGated(t, "deployer", deployBundle(t))
	require.Equal(t, "needs_review", pub.Decision.Outcome)
	require.True(t, pub.Created, "a held publish must still create the version row")
	require.NotEmpty(t, pub.Decision.Reasons)
	require.Equal(t, "execution", pub.Decision.Reasons[0].Class)

	// An evaluation was enqueued: without one, nothing could ever clear it.
	require.Equal(t, "pending", pub.Quality.State)
	require.NotEmpty(t, pub.Quality.JobID)

	// 2. It is invisible to every latest-resolving path.
	assert.NotContains(t, srv.manifestNames(t), "deployer")
	assert.Equal(t, 0, srv.latestVersion(t, "deployer"),
		"a held version must not advance latest_version")
	// The skill row itself is visible in list/search because the client
	// created it before publishing — that predates the gate. What must not
	// leak is a servable version, and latest_version is how every client
	// resolves one.
	for _, name := range srv.listSkillNames(t) {
		if name == "deployer" {
			var sk skill.Skill
			srv.getJSON(t, "/api/skills/deployer", &sk)
			assert.Equal(t, 0, sk.LatestVersion, "listed skill must advertise no servable version")
		}
	}

	// 3. The worker can still fetch the bundle by explicit version. If this
	//    fails, nothing can ever clear and the gate is a permanent hold.
	assert.Equal(t, http.StatusOK, srv.downloadVersion(t, "deployer", pub.Version).Code)

	// 4. A verified report above the floor releases it.
	srv.runEval(t, 82)

	// The score really did land, against the suite the test pushed.
	q := srv.getQuality(t, "deployer")
	require.True(t, q.Verified)
	require.Equal(t, ref, q.SuiteRef)

	// 5. Now it is served.
	assert.Contains(t, srv.manifestNames(t), "deployer")
	assert.Contains(t, srv.listSkillNames(t), "deployer")
	assert.Contains(t, srv.searchNames(t, "deployer"), "deployer")
	assert.Equal(t, pub.Version, srv.latestVersion(t, "deployer"))
	assert.Equal(t, http.StatusOK, srv.downloadVersion(t, "deployer", pub.Version).Code)
	assert.Equal(t, "released", srv.getVersion(t, "deployer", pub.Version).GateState)
}

// TestGateSecretFindingStaysBlocked is the guarantee that makes the rest
// safe: no score and no override buys a credential.
func TestGateSecretFindingStaysBlocked(t *testing.T) {
	srv := startServer(t)

	srv.createSkill(t, "leaky")
	// Published by the instance owner, explicitly asking to override.
	resp := srv.publishRaw(t, "leaky", secretBundle(t), "?override=true")
	require.Equal(t, http.StatusUnprocessableEntity, resp.Code, "body: %s", resp.Body)
	assert.Contains(t, resp.Body, "unappealable")

	// No version row exists, so there is nothing an evaluation could clear.
	var body struct {
		Versions []skill.Version `json:"versions"`
	}
	srv.getJSON(t, "/api/skills/leaky/versions", &body)
	assert.Empty(t, body.Versions, "a blocked publish must leave no version row")
	assert.Equal(t, 0, srv.latestVersion(t, "leaky"))
}

// TestGateAdminApprovalReleases covers the path an operator uses when the
// eval is wrong or no worker is running.
func TestGateAdminApprovalReleases(t *testing.T) {
	srv := startServer(t)

	srv.createSkill(t, "manual")
	pub := srv.publishGated(t, "manual", deployBundle(t))
	require.Equal(t, "needs_review", pub.Decision.Outcome)
	assert.NotContains(t, srv.manifestNames(t), "manual")

	got := srv.review(t, "manual", pub.Version, "approve", "read it line by line")
	require.Equal(t, http.StatusOK, got.Code, "body: %s", got.Body)

	assert.Contains(t, srv.manifestNames(t), "manual")
	assert.Equal(t, pub.Version, srv.latestVersion(t, "manual"))
	assert.Equal(t, "released", srv.getVersion(t, "manual", pub.Version).GateState)
}

// TestGateBelowFloorStaysHeld: a score is not automatically a pass.
func TestGateBelowFloorStaysHeld(t *testing.T) {
	srv := startServerWithFloor(t, 70)

	srv.createSkill(t, "weak")
	srv.pushSuite(t, "weak", fixtureSuiteArchive(t))
	pub := srv.publishGated(t, "weak", deployBundle(t))
	require.Equal(t, "needs_review", pub.Decision.Outcome)

	srv.runEval(t, 41)

	// The score landed — the hold is the gate's judgement, not a missing
	// measurement.
	require.True(t, srv.getQuality(t, "weak").Verified)

	assert.NotContains(t, srv.manifestNames(t), "weak")
	assert.Equal(t, 0, srv.latestVersion(t, "weak"))
	assert.Equal(t, "needs_review", srv.getVersion(t, "weak", pub.Version).GateState)
}

// TestGateIncompletePanelStaysHeld: an above-floor score from a panel that
// did not fully run is not evidence. A member whose adapter failed its health
// probe contributes no result, and letting that clear a gate turns an expired
// token into a publish approval.
func TestGateIncompletePanelStaysHeld(t *testing.T) {
	srv := startServerWithFloor(t, 70)

	srv.createSkill(t, "partial")
	srv.pushSuite(t, "partial", fixtureSuiteArchive(t))
	pub := srv.publishGated(t, "partial", deployBundle(t))
	require.Equal(t, "needs_review", pub.Decision.Outcome)

	w := srv.newWorker(t, gateRunner{headline: 95, panelComplete: false})
	worked, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	require.True(t, worked)

	assert.NotContains(t, srv.manifestNames(t), "partial")
	assert.Equal(t, "needs_review", srv.getVersion(t, "partial", pub.Version).GateState)
}
