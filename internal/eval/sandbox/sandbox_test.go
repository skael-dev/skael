package sandbox_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/sprites"
)

// fakeDriver is a Driver that records what it was asked to do. Every package
// downstream of sandbox tests against one of these; it lives in the test file
// of each so no test binary depends on another's fixtures.
type fakeDriver struct {
	isolated bool
	runs     []sandbox.RunSpec
}

func (f *fakeDriver) Name() string           { return "fake" }
func (f *fakeDriver) HardwareIsolated() bool { return f.isolated }
func (f *fakeDriver) Prepare(context.Context, sandbox.EnvSpec) (sandbox.ImageRef, error) {
	return sandbox.ImageRef{Tag: "fake:latest", DepsDigest: "d"}, nil
}
func (f *fakeDriver) Snapshot(context.Context, sandbox.ImageRef) (sandbox.SnapshotRef, error) {
	return sandbox.SnapshotRef{}, nil
}
func (f *fakeDriver) Run(_ context.Context, rs sandbox.RunSpec) (sandbox.RunResult, error) {
	f.runs = append(f.runs, rs)
	return sandbox.RunResult{}, nil
}

func validSpec() sandbox.RunSpec {
	return sandbox.RunSpec{
		Image:     sandbox.ImageRef{Tag: "t"},
		Workspace: "/tmp/ws",
		Argv:      []string{"true"},
		Network:   sandbox.NetNone,
		Timeout:   time.Minute,
	}
}

func TestCheckPolicy_UntrustedRefusesASharedKernelDriver(t *testing.T) {
	err := sandbox.CheckPolicy(&fakeDriver{isolated: false}, true)
	if !errors.Is(err, sandbox.ErrUntrustedRequiresIsolation) {
		t.Fatalf("err = %v, want ErrUntrustedRequiresIsolation", err)
	}
	// Fail closed. A container shares the host kernel, and an escape takes the
	// worker host along with its LLM key and every other tenant's run.
	if err := sandbox.CheckPolicy(&fakeDriver{isolated: true}, true); err != nil {
		t.Errorf("hardware-isolated driver refused untrusted work: %v", err)
	}
	if err := sandbox.CheckPolicy(&fakeDriver{isolated: false}, false); err != nil {
		t.Errorf("shared-kernel driver refused trusted work: %v", err)
	}
}

func TestGated_RefusesUntrustedWorkAtConstructionForASharedKernelDriver(t *testing.T) {
	_, err := sandbox.Gated(&fakeDriver{isolated: false}, true)
	if !errors.Is(err, sandbox.ErrUntrustedRequiresIsolation) {
		t.Fatalf("err = %v, want ErrUntrustedRequiresIsolation", err)
	}
}

func TestGated_ReturnsTheDriverUnchangedForTrustedWork(t *testing.T) {
	fd := &fakeDriver{isolated: false}
	d, err := sandbox.Gated(fd, false)
	if err != nil {
		t.Fatalf("Gated: %v", err)
	}
	if d != sandbox.Driver(fd) {
		t.Error("Gated wrapped a driver that trusted work never needed wrapped")
	}
}

// TestGated_WrapperKeepsRefusingEvenAfterEscapingIntoAField is the case the
// wrapper exists for: a caller that stashes the *wrapped* driver into a
// struct field, passes it somewhere that has never heard of CheckPolicy, and
// calls Run directly. The refusal must still fire from inside Run, not just
// at Gated's own construction time.
func TestGated_WrapperKeepsRefusingEvenAfterEscapingIntoAField(t *testing.T) {
	fd := &fakeDriver{isolated: true}
	d, err := sandbox.Gated(fd, true)
	if err != nil {
		t.Fatalf("Gated: %v", err)
	}

	type holder struct{ d sandbox.Driver }
	h := holder{d: d}

	// Flip the underlying driver's isolation after construction — nothing
	// stops it, and the wrapper is what re-checks on every Run rather than
	// trusting a decision made once.
	fd.isolated = false
	_, err = h.d.Run(context.Background(), validSpec())
	if !errors.Is(err, sandbox.ErrUntrustedRequiresIsolation) {
		t.Fatalf("Run err = %v, want ErrUntrustedRequiresIsolation", err)
	}
}

func TestRunSpec_Validate(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*sandbox.RunSpec)
		want string
	}{
		{"no argv", func(rs *sandbox.RunSpec) { rs.Argv = nil }, "argv"},
		{"no image", func(rs *sandbox.RunSpec) { rs.Image = sandbox.ImageRef{} }, "image"},
		{"no workspace", func(rs *sandbox.RunSpec) { rs.Workspace = "" }, "workspace"},
		{"no timeout", func(rs *sandbox.RunSpec) { rs.Timeout = 0 }, "timeout"},
		{"unknown policy", func(rs *sandbox.RunSpec) { rs.Network = "sometimes" }, "network policy"},
		// An allowlist with nothing on it is not "deny everything" — it is a
		// policy whose author believed they had permitted something. Reporting
		// it is the difference between a configuration bug and a mystery.
		{"empty allowlist", func(rs *sandbox.RunSpec) { rs.Network = sandbox.NetAllowlist }, "allowlist"},
		// Domains on a policy that ignores them is the mirror image: the author
		// believes traffic is restricted when nothing restricts it.
		{"domains without allowlist", func(rs *sandbox.RunSpec) {
			rs.Network = sandbox.NetNone
			rs.Allow = []string{"api.anthropic.com"}
		}, "allow"},
		{"relative workspace", func(rs *sandbox.RunSpec) { rs.Workspace = "ws" }, "absolute"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rs := validSpec()
			c.mut(&rs)
			err := rs.Validate()
			if err == nil {
				t.Fatalf("Validate accepted %s", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}

	rs := validSpec()
	rs.Network = sandbox.NetAllowlist
	rs.Allow = []string{"api.anthropic.com"}
	if err := rs.Validate(); err != nil {
		t.Errorf("Validate rejected a well-formed allowlist spec: %v", err)
	}
}

func TestSprites_IsInterfaceConformingAndFailsLoudly(t *testing.T) {
	var d sandbox.Driver = sprites.New()
	if !d.HardwareIsolated() {
		t.Error("sprites reports a shared kernel; untrusted work would then have no driver at all")
	}
	if _, err := d.Prepare(context.Background(), sandbox.EnvSpec{}); !errors.Is(err, sandbox.ErrDriverNotImplemented) {
		t.Errorf("Prepare err = %v, want ErrDriverNotImplemented", err)
	}
	if _, err := d.Run(context.Background(), validSpec()); !errors.Is(err, sandbox.ErrDriverNotImplemented) {
		t.Errorf("Run err = %v, want ErrDriverNotImplemented", err)
	}
}
