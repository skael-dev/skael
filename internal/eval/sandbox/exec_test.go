package sandbox_test

import (
	"context"
	"io"
	"testing"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

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
