package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVersionNotice(t *testing.T) {
	require.Contains(t, versionNotice("0.20.0", "0.21.0"), "older than this server (0.21.0)")
	require.Contains(t, versionNotice("v0.21.0", "0.20.1"), "server upgraded")
	require.Empty(t, versionNotice("0.21.0", "0.21.0"))
	require.Empty(t, versionNotice("0.21.0", "0.21.0-e2e"), "a build suffix is the same release")
	require.Empty(t, versionNotice("dev", "0.21.0"), "a dev build compares with nothing")
	require.Empty(t, versionNotice("0.21.0", "dev"))
}

// The check runs at most once a day and prints to stderr only, so a script
// reading stdout and an auto-sync hook see nothing new.
func TestMaybeNoticeVersion_OncePerDayOnStderr(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"version":"0.22.0"}`)
	}))
	defer srv.Close()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SKAEL_URL", srv.URL)
	t.Setenv("SKAEL_KEY", "k")
	old := buildVersion
	buildVersion = "0.21.0"
	defer func() { buildVersion = old }()

	stdout, stderr := capture(t, func() { maybeNoticeVersion([]string{"list"}) })
	require.Empty(t, stdout)
	require.Contains(t, stderr, "older than this server (0.22.0)")

	_, stderr = capture(t, func() { maybeNoticeVersion([]string{"list"}) })
	require.Empty(t, stderr, "a second run the same day stays quiet")
	require.EqualValues(t, 1, calls.Load())

	_, stderr = capture(t, func() { maybeNoticeVersion([]string{"sync", "--quiet"}) })
	require.Empty(t, stderr)
}

func capture(t *testing.T, fn func()) (string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	ro, wo, _ := os.Pipe()
	re, we, _ := os.Pipe()
	os.Stdout, os.Stderr = wo, we
	fn()
	wo.Close()
	we.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	var o, e bytes.Buffer
	_, _ = io.Copy(&o, ro)
	_, _ = io.Copy(&e, re)
	return strings.TrimSpace(o.String()), strings.TrimSpace(e.String())
}
