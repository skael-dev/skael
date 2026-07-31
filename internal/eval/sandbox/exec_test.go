package sandbox_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

// errDriver returns a fixed RunResult alongside a non-nil error, the shape a
// cancelled or driver-failed docker run produces: Run does not promise a
// zero-value RunResult just because it also returns an error.
type errDriver struct{ fakeDriver }

func (d *errDriver) Run(context.Context, sandbox.RunSpec) (sandbox.RunResult, error) {
	return sandbox.RunResult{ExitCode: 7}, errors.New("boom")
}

func TestExec_ReturnsTheDriversExitCodeEvenOnError(t *testing.T) {
	d := &errDriver{}
	ex := sandbox.NewExec(d, validSpec())
	code, err := ex.Exec(context.Background(), []string{"claude"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("Exec: want an error")
	}
	// A caller logging both the exit code and the error must see the driver's
	// real exit code, not a fabricated 0 that reads as a clean exit.
	if code != 7 {
		t.Errorf("code = %d, want 7 (the driver's own ExitCode, not a fabricated 0)", code)
	}
}

func TestNewExec_SubstitutesArgvAndKeepsTheRestOfTheSpec(t *testing.T) {
	d := &fakeDriver{}
	base := validSpec()
	base.Argv = []string{"placeholder"}
	base.Network = sandbox.NetAllowlist
	base.Allow = []string{"api.anthropic.com"}

	ex := sandbox.NewExec(d, base)
	if _, err := ex.Exec(context.Background(), []string{"claude", "-p", "x"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if len(d.runs) != 1 {
		t.Fatalf("driver saw %d runs", len(d.runs))
	}
	got := d.runs[0]
	if got.Argv[0] != "claude" {
		t.Errorf("argv = %v, want the caller's", got.Argv)
	}
	// An executor that lost the network policy would run every agent session
	// with no egress, and every session would fail for a reason that looks like
	// the agent's fault.
	if got.Network != sandbox.NetAllowlist || len(got.Allow) != 1 {
		t.Errorf("network policy lost: %q %v", got.Network, got.Allow)
	}
	if got.Workspace != base.Workspace || got.Timeout != base.Timeout {
		t.Errorf("spec fields lost: %+v", got)
	}
}
