//go:build unix

package northflank

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
)

// recordingAPI is a fake Northflank that records the calls it received.
type recordingAPI struct {
	mu       sync.Mutex
	created  int
	deleted  int
	execCode int
	execFail bool
}

func (a *recordingAPI) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		switch {
		case r.Method == http.MethodDelete:
			a.deleted++
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/exec"):
			if a.execFail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"commandResult": map[string]any{"exitCode": a.execCode}},
			})
		case r.Method == http.MethodPost:
			a.created++
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "svc-1"}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"id": "svc-1", "status": map[string]any{"deployment": map[string]any{"status": "COMPLETED"}}},
			})
		}
	}
}

func runDriver(t *testing.T, o Options, api *recordingAPI) *Driver {
	t.Helper()
	srv := httptest.NewServer(api.handler())
	t.Cleanup(srv.Close)
	d, err := New(o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.c.base = srv.URL
	d.waitInterval = time.Millisecond
	return d
}

func runnableSpec(t *testing.T) sandbox.RunSpec {
	t.Helper()
	return sandbox.RunSpec{
		// Must match validOptions().withDefaults().Image, since Run now
		// refuses a mismatch between rs.Image and the driver's own image.
		Image:     sandbox.ImageRef{Tag: imagespec.PublishedBaseImage},
		Workspace: t.TempDir(),
		Argv:      []string{"claude", "-p", "go"},
		Network:   sandbox.NetFull,
		Timeout:   time.Minute,
	}
}

func TestRun_ReturnsTheCommandsExitCodeAsAResult(t *testing.T) {
	fakeCLI(t, 0)
	api := &recordingAPI{execCode: 3}
	d := runDriver(t, validOptions(), api)

	res, err := d.Run(context.Background(), runnableSpec(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3; a non-zero exit is a result, not an error", res.ExitCode)
	}
}

// A leaked sandbox is a bill that grows while nobody looks at it, so deletion
// must happen on every path, not only the happy one.
func TestRun_DeletesTheSandboxOnEveryExitPath(t *testing.T) {
	for _, tc := range []struct {
		name     string
		execFail bool
		cliExit  int
	}{
		{"clean exit", false, 0},
		{"exec fails", true, 0},
		{"workspace transfer fails", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeCLI(t, tc.cliExit)
			api := &recordingAPI{execFail: tc.execFail}
			d := runDriver(t, validOptions(), api)

			_, _ = d.Run(context.Background(), runnableSpec(t))

			api.mu.Lock()
			defer api.mu.Unlock()
			if api.created != api.deleted {
				t.Errorf("created %d sandboxes and deleted %d; a leaked sandbox bills until someone notices", api.created, api.deleted)
			}
		})
	}
}

func TestRun_RefusesARestrictedRunWithoutTheEnforcementAssertion(t *testing.T) {
	fakeCLI(t, 0)
	d := runDriver(t, validOptions(), &recordingAPI{})
	rs := runnableSpec(t)
	rs.Network = sandbox.NetNone

	_, err := d.Run(context.Background(), rs)
	if !errors.Is(err, ErrNetworkPolicyUnenforced) {
		t.Fatalf("Run = %v, want ErrNetworkPolicyUnenforced", err)
	}
}

// The refusal must happen before anything is created, or a refused run still
// costs money.
func TestRun_CreatesNothingWhenItRefusesTheRun(t *testing.T) {
	fakeCLI(t, 0)
	api := &recordingAPI{}
	d := runDriver(t, validOptions(), api)
	rs := runnableSpec(t)
	rs.Network = sandbox.NetAllowlist
	rs.Allow = []string{"evil.example.com"}

	_, _ = d.Run(context.Background(), rs)

	api.mu.Lock()
	defer api.mu.Unlock()
	if api.created != 0 {
		t.Errorf("created %d sandboxes for a refused run; a refusal must cost nothing", api.created)
	}
}

func TestRun_RefusesAHostMountWithAMessageNamingTheAlternative(t *testing.T) {
	fakeCLI(t, 0)
	d := runDriver(t, validOptions(), &recordingAPI{})
	rs := runnableSpec(t)
	rs.Mounts = []sandbox.Mount{{HostPath: "/home/u/.claude", ContainerPath: "/home/runner/.claude", ReadOnly: true}}

	_, err := d.Run(context.Background(), rs)
	if err == nil || !strings.Contains(err.Error(), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("Run = %v, want a refusal naming CLAUDE_CODE_OAUTH_TOKEN", err)
	}
}

func TestRun_RefusesAnImageMismatchRatherThanSubstitutingItsOwn(t *testing.T) {
	fakeCLI(t, 0)
	d := runDriver(t, validOptions(), &recordingAPI{})
	rs := runnableSpec(t)
	rs.Image = sandbox.ImageRef{Tag: "ghcr.io/other/image:9"}

	_, err := d.Run(context.Background(), rs)
	if err == nil || !strings.Contains(err.Error(), "ghcr.io/other/image:9") {
		t.Fatalf("Run = %v, want a refusal naming the mismatched image", err)
	}
}

func TestRun_RefusesStdinRatherThanDroppingIt(t *testing.T) {
	fakeCLI(t, 0)
	d := runDriver(t, validOptions(), &recordingAPI{})
	rs := runnableSpec(t)
	rs.Stdin = strings.NewReader("input")

	_, err := d.Run(context.Background(), rs)
	if err == nil || !strings.Contains(err.Error(), "stdin") {
		t.Fatalf("Run = %v, want a refusal naming stdin", err)
	}
}

func TestRun_MarksACancelledRunCancelledRatherThanFailed(t *testing.T) {
	fakeCLI(t, 0)
	d := runDriver(t, validOptions(), &recordingAPI{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := d.Run(ctx, runnableSpec(t))
	if err == nil {
		t.Fatal("Run: want an error for a cancelled run")
	}
	if !res.Cancelled {
		t.Error("Cancelled must be true; a cancelled run must never record as a failure")
	}
}

func TestSweep_DeletesOrphansCarryingTheOwnerLabel(t *testing.T) {
	fakeCLI(t, 0)
	api := &recordingAPI{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			api.mu.Lock()
			api.deleted++
			api.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"services": []any{
			map[string]any{"id": "svc-orphan", "name": "whetstone-run-old", "metadata": map[string]any{"labels": map[string]any{ownerLabelKey: "skael"}}},
		}}})
	}))
	t.Cleanup(srv.Close)

	d, _ := New(validOptions())
	d.c.base = srv.URL
	d.Sweep(context.Background())

	api.mu.Lock()
	defer api.mu.Unlock()
	if api.deleted != 1 {
		t.Errorf("swept %d orphans, want 1", api.deleted)
	}
}
