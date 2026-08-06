package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordedCall is one HTTP request the stub server observed.
type recordedCall struct {
	Path   string
	Body   string
	Method string
}

func recordingReviewServer(t *testing.T) (*httptest.Server, *[]recordedCall) {
	t.Helper()
	calls := &[]recordedCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*calls = append(*calls, recordedCall{Path: r.URL.Path, Body: string(body), Method: r.Method})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"version": 3, "checksum": "abc"})
	}))
	return srv, calls
}

// resetReviewFlags clears the package-level flag vars review.go's init()
// wires to cobra, since they persist across test invocations in the same
// process.
func resetReviewFlags() {
	reviewApprove = false
	reviewReject = false
	reviewReason = ""
	reviewReasonKind = ""
}

func TestReviewCommandCallsTheEndpoint(t *testing.T) {
	resetReviewFlags()
	srv, calls := recordingReviewServer(t)
	defer srv.Close()
	t.Setenv("SKAEL_URL", srv.URL)
	t.Setenv("SKAEL_KEY", "test-key")

	reviewApprove = true
	reviewReason = "checked by hand"

	captureStderr(t, func() {
		captureStdout(t, func() {
			err := runReview(reviewCmd, []string{"my-skill", "3"})
			require.NoError(t, err)
		})
	})

	require.Len(t, *calls, 1)
	assert.Equal(t, "/api/skills/my-skill/versions/3/review", (*calls)[0].Path)
	assert.JSONEq(t, `{"action":"approve","reason":"checked by hand"}`, (*calls)[0].Body)
}

func TestReviewRequiresAReasonLocally(t *testing.T) {
	resetReviewFlags()
	reviewApprove = true

	err := runReview(reviewCmd, []string{"my-skill", "3"})
	assert.ErrorContains(t, err, "reason")
}

func TestReviewRejectsBothFlags(t *testing.T) {
	resetReviewFlags()
	reviewApprove = true
	reviewReject = true
	reviewReason = "x"

	err := runReview(reviewCmd, []string{"my-skill", "3"})
	assert.Error(t, err)
}

func TestReviewRejectsNeitherFlag(t *testing.T) {
	resetReviewFlags()
	reviewReason = "x"

	err := runReview(reviewCmd, []string{"my-skill", "3"})
	assert.Error(t, err)
}

// The specific way a two-reason hold would otherwise lie: clearing one
// reason must never be reported as "released" while another is still
// outstanding.
func TestReviewNeverReportsReleasedWhenAReasonRemains(t *testing.T) {
	resetReviewFlags()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version":      3,
			"checksum":     "abc",
			"gate_state":   "needs_review",
			"hold_reasons": []string{"scan", "ownership"},
		})
	}))
	defer srv.Close()
	t.Setenv("SKAEL_URL", srv.URL)
	t.Setenv("SKAEL_KEY", "test-key")

	reviewApprove = true
	reviewReason = "looks right"
	reviewReasonKind = "ownership"

	out := captureStderr(t, func() {
		captureStdout(t, func() {
			err := runReview(reviewCmd, []string{"my-skill", "3"})
			require.NoError(t, err)
		})
	})

	assert.NotContains(t, strings.ToLower(out), "released",
		"ownership cleared but scan is still outstanding — this must not read as released")
	assert.Contains(t, out, "scan", "the remaining reason must be named")
	assert.Contains(t, out, "owner or admin", "and who can clear it")
}

// One outstanding reason keeps working without --reason-kind, so every
// deployed `skael review --approve` is unaffected.
func TestReviewOmitsReasonWhenUnambiguous(t *testing.T) {
	resetReviewFlags()
	srv, calls := recordingReviewServer(t)
	defer srv.Close()
	t.Setenv("SKAEL_URL", srv.URL)
	t.Setenv("SKAEL_KEY", "test-key")

	reviewApprove = true
	reviewReason = "checked by hand"
	// reviewReasonKind deliberately left empty.

	captureStderr(t, func() {
		captureStdout(t, func() {
			err := runReview(reviewCmd, []string{"my-skill", "3"})
			require.NoError(t, err)
		})
	})

	require.Len(t, *calls, 1)
	assert.JSONEq(t, `{"action":"approve","reason":"checked by hand"}`, (*calls)[0].Body,
		"no hold_reason key when --reason-kind is unset")
}

// --reason-kind, when given, must name one of the two hold reasons that
// exist (scan, ownership). Anything else is a local error caught before any
// request is sent — the same "fail fast" discipline already applied to
// --approve/--reject and --reason above, since the server would only 422 on
// exactly this after a wasted round trip.
func TestReviewRequiresReasonWhenTwoOutstanding(t *testing.T) {
	resetReviewFlags()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// t.Errorf, not t.Fatalf: this runs on the server's own goroutine,
		// where Fatalf's runtime.Goexit is unsafe to call.
		t.Errorf("unexpected request to %s — an invalid --reason-kind must be rejected locally", r.URL.Path)
	}))
	defer srv.Close()
	t.Setenv("SKAEL_URL", srv.URL)
	t.Setenv("SKAEL_KEY", "test-key")

	reviewApprove = true
	reviewReason = "x"
	reviewReasonKind = "bogus"

	err := runReview(reviewCmd, []string{"my-skill", "3"})
	assert.ErrorContains(t, err, "reason-kind")
}

// `review show` prints the diff and the outstanding reasons.
func TestReviewShowPrintsDiff(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/review/queue", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"held": []map[string]any{
				{
					"skill_name":   "my-skill",
					"version":      3,
					"gate_state":   "needs_review",
					"hold_reasons": []string{"scan"},
					"outstanding":  []string{"scan"},
				},
			},
			"total": 1,
		})
	})
	mux.HandleFunc("/api/skills/my-skill/versions/3/diff", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"against":  2,
			"skill_md": "--- v2/SKILL.md\n+++ v3/SKILL.md\n@@ -1 +1 @@\n-old\n+new\n",
			"files":    []map[string]any{},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("SKAEL_URL", srv.URL)
	t.Setenv("SKAEL_KEY", "test-key")

	var out string
	errOut := captureStderr(t, func() {
		out = captureStdout(t, func() {
			err := runReviewShow(reviewShowCmd, []string{"my-skill", "3"})
			require.NoError(t, err)
		})
	})
	out += errOut

	assert.Contains(t, out, "-old")
	assert.Contains(t, out, "+new")
}
