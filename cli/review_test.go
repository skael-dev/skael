package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
