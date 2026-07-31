package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it. publish.go's success/held rendering writes there.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	require.NoError(t, w.Close())
	b, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(b)
}

// captureStderr redirects os.Stderr for the duration of fn and returns
// everything written to it. ui.Error/ui.Errorf/ui.Info write there.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	require.NoError(t, w.Close())
	b, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(b)
}

// skillDir builds a minimal valid skill directory to publish.
func skillDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: my-skill\ndescription: test skill\n---\n# my-skill\n"), 0o644))
	return dir
}

// stubPublishServer starts an httptest.Server that answers the skill-exists
// check, skill creation, and version publish calls needed by runPublish. The
// versionHandler is invoked for POST .../versions and controls the response.
func stubPublishServer(t *testing.T, versionHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/skills/my-skill", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
	})
	mux.HandleFunc("/api/skills", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "my-skill"})
	})
	mux.HandleFunc("/api/skills/my-skill/versions", versionHandler)
	return httptest.NewServer(mux)
}

// runPublishAgainst points the CLI at srv via env vars and runs runPublish
// against dir, returning combined stdout+stderr.
func runPublishAgainst(t *testing.T, srv *httptest.Server, dir string) string {
	t.Helper()
	t.Setenv("SKAEL_URL", srv.URL)
	t.Setenv("SKAEL_KEY", "test-key")

	var out, errOut string
	errOut = captureStderr(t, func() {
		out = captureStdout(t, func() {
			err := runPublish(publishCmd, []string{dir})
			require.NoError(t, err)
		})
	})
	return out + errOut
}

func heldPublishBody() []byte {
	body := struct {
		Version  int           `json:"version"`
		Checksum string        `json:"checksum"`
		Created  bool          `json:"created"`
		Decision gate.Decision `json:"decision"`
	}{
		Version:  3,
		Checksum: "abc123",
		Created:  true,
		Decision: gate.Decision{
			Outcome: gate.NeedsReview,
			Reasons: []gate.Reason{{
				Rule: "curl-pipe-sh", Class: "execution", Severity: "high",
				File: "install.sh", Line: 12,
				Message: "piping a download into a shell",
				Clears:  "a verified evaluation scoring at least 60 with a complete panel and no critical contract violations, or an admin approval",
			}},
		},
	}
	b, _ := json.Marshal(body)
	return b
}

func TestPublishRendersHeld(t *testing.T) {
	srv := stubPublishServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(heldPublishBody())
	})
	defer srv.Close()

	out := runPublishAgainst(t, srv, skillDir(t))

	assert.NotContains(t, strings.ToLower(out), "published successfully",
		"a held version is not published; saying so is the failure this test exists to prevent")
	assert.NotContains(t, strings.ToLower(out), "✓ published",
		"a held version must not print the plain success line")
	assert.Contains(t, out, "held for review")
	assert.Contains(t, out, "install.sh:12")
	assert.Contains(t, out, "admin approval", "the output must say how to clear it")
}

func TestPublishJSONCarriesTheDecision(t *testing.T) {
	srv := stubPublishServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(heldPublishBody())
	})
	defer srv.Close()

	ui.JSONMode = true
	t.Cleanup(func() { ui.JSONMode = false })
	out := runPublishAgainst(t, srv, skillDir(t))

	var got struct {
		Decision gate.Decision `json:"decision"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, gate.NeedsReview, got.Decision.Outcome)
	require.Len(t, got.Decision.Reasons, 1)
	assert.Equal(t, "curl-pipe-sh", got.Decision.Reasons[0].Rule)
}

func TestPublishRendersBlockedDistinctlyFromHeld(t *testing.T) {
	blockDecision := gate.Decision{
		Outcome: gate.Block,
		Reasons: []gate.Reason{{
			Rule: "aws-secret-key", Class: "secret", Severity: "critical",
			File: "config.yaml", Line: 4,
			Message: "AWS secret access key",
			Clears:  "nothing: credential-theft findings are unappealable. Remove the finding from the bundle; no evaluation and no override clears it.",
		}},
	}
	payload, _ := json.Marshal(struct {
		Scan     *scan.Report  `json:"scan"`
		Decision gate.Decision `json:"decision"`
	}{Scan: &scan.Report{Status: "critical"}, Decision: blockDecision})

	srv := stubPublishServer(t, func(w http.ResponseWriter, r *http.Request) {
		envelope := struct {
			Title  string `json:"title"`
			Detail string `json:"detail"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}{
			Title: "archive rejected",
			Errors: []struct {
				Message string `json:"message"`
			}{{Message: string(payload)}},
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(envelope)
	})
	defer srv.Close()

	out := runPublishAgainst(t, srv, skillDir(t))

	assert.Contains(t, out, "unappealable")
	assert.NotContains(t, out, "admin approval",
		"offering an override for a finding no override clears sends the operator on a wild goose chase")
	assert.NotContains(t, out, "--override",
		"a block outcome must not suggest --override; nothing clears it")
}

func TestPublishExitCodeHeld(t *testing.T) {
	// A held publish is not a failure — the version was created — so
	// runPublish must return nil (exit code 0), which runPublishAgainst
	// already asserts via require.NoError. This test exists to name that
	// requirement explicitly.
	srv := stubPublishServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(heldPublishBody())
	})
	defer srv.Close()

	t.Setenv("SKAEL_URL", srv.URL)
	t.Setenv("SKAEL_KEY", "test-key")
	captureStderr(t, func() {
		captureStdout(t, func() {
			err := runPublish(publishCmd, []string{skillDir(t)})
			assert.NoError(t, err)
		})
	})
}

func TestPublishUnchangedChecksumHeldNotAlarming(t *testing.T) {
	body := struct {
		Version  int           `json:"version"`
		Checksum string        `json:"checksum"`
		Created  bool          `json:"created"`
		Decision gate.Decision `json:"decision"`
	}{
		Version:  3,
		Checksum: "abc123",
		Created:  false,
		Decision: gate.Decision{
			Outcome: gate.NeedsReview,
			Reasons: []gate.Reason{{Rule: "curl-pipe-sh", Class: "execution", Severity: "high"}},
		},
	}
	b, _ := json.Marshal(body)

	srv := stubPublishServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(b)
	})
	defer srv.Close()

	out := runPublishAgainst(t, srv, skillDir(t))

	assert.NotContains(t, strings.ToLower(out), "created and held for review",
		"an unchanged republish did not just newly hold anything")
	assert.Contains(t, out, "No changes detected")
}
