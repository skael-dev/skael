//go:build docker

package docker_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/docker"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
	evalspec "github.com/skael-dev/skael/internal/eval/spec"
)

// driver builds the base image once per test binary. WHETSTONE_BASE_TAG lets
// CI point at the slim image; locally the default full image is used.
func driver(t *testing.T) *docker.Driver {
	t.Helper()
	slim := os.Getenv("WHETSTONE_BASE_TAG") == imagespec.SlimBaseTag
	d, err := docker.New(docker.Options{BaseTag: os.Getenv("WHETSTONE_BASE_TAG"), Logger: t.Logf})
	if err != nil {
		t.Fatalf("docker.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if err := d.EnsureBase(ctx, slim); err != nil {
		t.Fatalf("EnsureBase: %v", err)
	}
	return d
}

func prepare(t *testing.T, d *docker.Driver) sandbox.ImageRef {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	ref, err := d.Prepare(ctx, sandbox.EnvSpec{Skill: "demo", BaseTag: os.Getenv("WHETSTONE_BASE_TAG")})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return ref
}

func run(t *testing.T, d *docker.Driver, rs sandbox.RunSpec) (sandbox.RunResult, string) {
	t.Helper()
	var out bytes.Buffer
	rs.Stdout, rs.Stderr = &out, &out
	res, err := d.Run(context.Background(), rs)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	return res, out.String()
}

func TestPrepare_IsContentAddressedAndCached(t *testing.T) {
	d := driver(t)
	a := prepare(t, d)
	start := time.Now()
	b := prepare(t, d)
	if a.Tag != b.Tag {
		t.Errorf("tags differ for identical env specs: %s vs %s", a.Tag, b.Tag)
	}
	// A cache hit is an image inspect, not a build. Without it a 60-session
	// tier rebuilds an identical layer for every run.
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("second Prepare took %s; the layer was rebuilt rather than reused", elapsed)
	}
}

func TestPrepare_BakesDeclaredDeps(t *testing.T) {
	d := driver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	ref, err := d.Prepare(ctx, sandbox.EnvSpec{
		Skill:   "demo",
		BaseTag: os.Getenv("WHETSTONE_BASE_TAG"),
		Deps:    evalspec.DepsDecl{Pip: []string{"tabulate"}},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	_, out := run(t, d, sandbox.RunSpec{
		Image: ref, Workspace: t.TempDir(), Network: sandbox.NetNone, Timeout: time.Minute,
		Argv: []string{"python3", "-c", "import tabulate; print('ok')"},
	})
	if !strings.Contains(out, "ok") {
		t.Errorf("declared pip dep is not importable in the run:\n%s", out)
	}
}

func TestRun_ReportsExitCodesAndWritesToTheWorkspace(t *testing.T) {
	d := driver(t)
	ref := prepare(t, d)
	ws := t.TempDir()

	res, _ := run(t, d, sandbox.RunSpec{
		Image: ref, Workspace: ws, Network: sandbox.NetNone, Timeout: time.Minute,
		Argv: []string{"sh", "-c", "echo hello > out.txt"},
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	// The workspace is the artifact channel. A run whose writes do not land on
	// the host produces a verifier that inspects nothing.
	b, err := os.ReadFile(filepath.Join(ws, "out.txt"))
	if err != nil || strings.TrimSpace(string(b)) != "hello" {
		t.Errorf("workspace file = %q, %v", b, err)
	}

	res, _ = run(t, d, sandbox.RunSpec{
		Image: ref, Workspace: ws, Network: sandbox.NetNone, Timeout: time.Minute,
		Argv: []string{"sh", "-c", "exit 3"},
	})
	// A non-zero exit is the measurement, not an error to propagate.
	if res.ExitCode != 3 {
		t.Errorf("exit = %d, want 3", res.ExitCode)
	}
}

func TestRun_TimesOutAndLeavesNoContainer(t *testing.T) {
	d := driver(t)
	ref := prepare(t, d)

	before := containerCount(t)
	res, _ := run(t, d, sandbox.RunSpec{
		Image: ref, Workspace: t.TempDir(), Network: sandbox.NetNone, Timeout: 3 * time.Second,
		Argv: []string{"sleep", "120"},
	})
	if !res.TimedOut {
		t.Error("TimedOut = false for a run that outlived its timeout")
	}
	// Sixty leaked containers per eval is a full disk, and --rm does not fire
	// when the client is killed by the context.
	if after := containerCount(t); after != before {
		t.Errorf("container count went %d -> %d; the timed-out container leaked", before, after)
	}
}

// TestRun_ParentCancellationIsAnErrorNotAFabricatedResult exercises the
// branch at run.go's "case ctx.Err() != nil" directly: a parent context
// cancelled out from under a run — not the run's own Timeout elapsing, which
// TestRun_TimesOutAndLeavesNoContainer already covers — used to leave
// "docker start -a"'s exec.ExitError (typically "signal: killed",
// ExitCode() == -1) to fall through into a fabricated RunResult{ExitCode:
// -1}, nil: a legitimate-looking measurement for a session that was never
// actually observed. Cancelling ctx here (rather than letting rs.Timeout
// fire) is what distinguishes this from the TimedOut path.
func TestRun_ParentCancellationIsAnErrorNotAFabricatedResult(t *testing.T) {
	d := driver(t)
	ref := prepare(t, d)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)

	var out bytes.Buffer
	res, err := d.Run(ctx, sandbox.RunSpec{
		Image: ref, Workspace: t.TempDir(), Network: sandbox.NetNone, Timeout: time.Minute,
		Argv: []string{"sleep", "30"}, Stdout: &out, Stderr: &out,
	})
	if err == nil {
		t.Fatalf("Run cancelled by its parent context returned no error; res = %+v", res)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap context.Canceled", err)
	}
	if !res.Cancelled {
		t.Error("RunResult.Cancelled = false for a parent-cancelled run")
	}
	if res.TimedOut {
		t.Error("RunResult.TimedOut = true for a parent cancellation, not the run's own Timeout elapsing")
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want the zero value — a cancelled run has no real measurement to report", res.ExitCode)
	}
}

// TestRun_MissingImageIsAnErrorAtCreateNotAFakedExitCode pins the create-side
// half of the create/start/wait split: "docker create" cannot run the
// image's own command at all, so its own failure — a missing image, an
// invalid flag, an unreadable bind-mount source — is unambiguously the
// docker client's or daemon's own condition, never something that could be
// confused for a container's exit code.
func TestRun_MissingImageIsAnErrorAtCreateNotAFakedExitCode(t *testing.T) {
	d := driver(t)
	ws := t.TempDir()

	var out bytes.Buffer
	res, err := d.Run(context.Background(), sandbox.RunSpec{
		Image:     sandbox.ImageRef{Tag: "whetstone-does-not-exist:none"},
		Workspace: ws, Network: sandbox.NetNone, Timeout: time.Minute,
		Argv: []string{"true"}, Stdout: &out, Stderr: &out,
	})
	if err == nil {
		t.Fatalf("Run against a missing image returned no error; res = %+v\n%s", res, out.String())
	}
	if res.ExitCode != 0 {
		t.Errorf("RunResult.ExitCode = %d on a create failure, want the zero value — there is no container to have an exit code", res.ExitCode)
	}
}

// TestRun_ANonExecutableCommandIsALegitimateResultNotAHardError is the case
// the create/start/wait split exists for. Before the split, this looked
// identical — same exit code, same "docker run" process exit — to a genuine
// docker CLI/daemon failure, and the conservative fallback that preceded the
// split treated exit 126 as always meaning the latter: a plausible
// model-authored oracle or verifier whose last command happens to not be
// executable would have hard-aborted suite.Check and perpetually retried in
// the runner, rather than being scored as "the oracle failed" the way a
// broken reference solution should be. "docker wait" reports this as the
// container's own exit status — the daemon itself classifies a failed exec
// as the container exiting 126, not as a create/start command failure — so
// it now comes back as a result: no error, ExitCode 126.
func TestRun_ANonExecutableCommandIsALegitimateResultNotAHardError(t *testing.T) {
	d := driver(t)
	ref := prepare(t, d)
	ws := t.TempDir()

	// /etc/hostname exists in the image and is a regular file with no
	// executable bit — running it directly is exactly the shape of failure
	// docker reports as exit 126.
	var out bytes.Buffer
	res, err := d.Run(context.Background(), sandbox.RunSpec{
		Image: ref, Workspace: ws, Network: sandbox.NetNone, Timeout: time.Minute,
		Argv: []string{"/etc/hostname"}, Stdout: &out, Stderr: &out,
	})
	if err != nil {
		t.Fatalf("Run of a non-executable command returned an error instead of a result: %v\n%s", err, out.String())
	}
	if res.ExitCode != 126 {
		t.Errorf("ExitCode = %d, want 126 (docker's own convention for a failed exec, reported as the container's exit status)", res.ExitCode)
	}
}

// containerCount is scoped to this test binary's own containers via
// docker.OwnerLabel(), not a "whetstone-run-" name prefix: CI runs the
// docker-tagged test suite without -p 1, so this package's tests and
// suite's docker-tagged tests create containers concurrently against the
// same daemon, and a name-prefix filter would count (and, before Sweep
// learned to filter the same way, could have removed) the other package's
// containers too.
func containerCount(t *testing.T) int {
	t.Helper()
	out, err := exec.Command("docker", "ps", "-a", "--filter", "label="+docker.OwnerLabel(), "-q").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	return len(strings.Fields(string(out)))
}

func TestRun_MountsAreReadOnlyAndTheNetworkIsOff(t *testing.T) {
	d := driver(t)
	ref := prepare(t, d)
	auth := t.TempDir()
	if err := os.WriteFile(filepath.Join(auth, "token"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, out := run(t, d, sandbox.RunSpec{
		Image: ref, Workspace: t.TempDir(), Network: sandbox.NetNone, Timeout: time.Minute,
		Mounts: []sandbox.Mount{{HostPath: auth, ContainerPath: "/mnt/auth", ReadOnly: true}},
		Argv:   []string{"sh", "-c", "echo x > /mnt/auth/token"},
	})
	// A skill that can rewrite mounted credentials persists across runs.
	if res.ExitCode == 0 {
		t.Errorf("write to a read-only mount succeeded:\n%s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(auth, "token")); string(b) != "secret" {
		t.Errorf("mounted file was modified: %q", b)
	}

	res, out = run(t, d, sandbox.RunSpec{
		Image: ref, Workspace: t.TempDir(), Network: sandbox.NetNone, Timeout: time.Minute,
		Argv: []string{"sh", "-c", "getent hosts example.com"},
	})
	if res.ExitCode == 0 {
		t.Errorf("DNS resolved under the none policy:\n%s", out)
	}
}
