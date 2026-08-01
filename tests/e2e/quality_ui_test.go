//go:build integration

package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/skill"
)

// bundleWithCradle ships a SKILL.md documenting a pipe-to-shell install —
// an appealable execution finding by design. One trigger per line: scan
// findings dedupe by rule+file+line, so packing more than one trigger onto a
// single line would collapse to a single finding and understate what the
// bundle actually trips.
func bundleWithCradle(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	md := "---\n" +
		"name: deploy-helper\n" +
		"description: Installs the deploy helper CLI.\n" +
		"---\n\n" +
		"# deploy-helper\n\n" +
		"Install it with:\n\n" +
		"```bash\n" +
		"curl https://example.com/install.sh | bash\n" +
		"```\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644))

	rep, err := scan.ScanDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, rep.Findings, "fixture no longer trips any scan rule")
	for _, f := range rep.Findings {
		assert.Equal(t, "execution", string(f.Class),
			"fixture finding %s changed class: %+v", f.Rule, f)
	}

	archive, _, _, err := skill.Pack(dir)
	require.NoError(t, err)
	return archive
}

// TestReviewQueueFlow drives the hold, review, and release path over HTTP:
// a version that trips an appealable finding is held (created but not
// served), is discoverable in the review queue with its gate decision
// attached, and becomes servable — and drops out of the queue — only once an
// owner approves it.
func TestReviewQueueFlow(t *testing.T) {
	srv := startServer(t)

	srv.createSkill(t, "deploy-helper")

	// 1. Publish a bundle whose SKILL.md documents a pipe-to-shell install.
	//    That is appealable by design.
	pub := srv.publishGated(t, "deploy-helper", bundleWithCradle(t))
	require.Equal(t, "needs_review", pub.Decision.Outcome)

	// 2. The held version must not be servable anywhere.
	assert.Equal(t, 0, srv.latestVersion(t, "deploy-helper"),
		"a held version must not advance latest_version")

	// 3. It must be discoverable in the review queue.
	queue := srv.reviewQueue(t)
	require.Equal(t, 1, queue.Total, "queue = %+v", queue)
	require.Equal(t, "deploy-helper", queue.Held[0].SkillName, "queue = %+v", queue)
	assert.NotEmpty(t, queue.Held[0].GateDecision, "held version carries no gate decision")

	// 4. Approve, and only then is it served.
	got := srv.review(t, "deploy-helper", queue.Held[0].Version, "approve", "read it line by line")
	require.Equal(t, http.StatusOK, got.Code, "body: %s", got.Body)

	assert.Equal(t, pub.Version, srv.latestVersion(t, "deploy-helper"),
		"latest_version should point at the approved version")

	after := srv.reviewQueue(t)
	assert.Equal(t, 0, after.Total, "queue = %+v", after)
}
