//go:build northflank

package northflank_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/resolve"
	"github.com/skael-dev/skael/internal/eval/sandbox/sandboxtest"
)

func liveDriver(t *testing.T) sandbox.Driver {
	t.Helper()
	if os.Getenv("SANDBOX_NF_TOKEN") == "" {
		t.Skip("no Northflank credentials configured")
	}
	d, err := resolve.FromEnv(os.Getenv).Build(t.Logf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return d
}

func TestNorthflankDriver_SatisfiesTheWorkspaceContract(t *testing.T) {
	sandboxtest.RunConformance(t, liveDriver(t), sandbox.EnvSpec{Skill: "conformance"})
}

// The control run comes first and must succeed. Without it, a probe that fails
// for an unrelated reason looks exactly like a blocked one, and the test
// certifies enforcement that is not there.
func TestNorthflankDriver_BlocksEgressTheProjectDoesNotAllow(t *testing.T) {
	d := liveDriver(t)
	ctx := context.Background()
	img, err := d.Prepare(ctx, sandbox.EnvSpec{Skill: "egress"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	probe := []string{"sh", "-c", "curl -sS -m 5 https://example.com >/dev/null"}
	control, err := d.Run(ctx, sandbox.RunSpec{
		Image: img, Workspace: t.TempDir(), Argv: probe,
		Network: sandbox.NetFull, Timeout: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("control run under NetFull: %v", err)
	}
	if control.ExitCode != 0 {
		t.Skipf("the probe itself does not work in this project (exit %d); the block below proves nothing", control.ExitCode)
	}

	var stderr strings.Builder
	blocked, err := d.Run(ctx, sandbox.RunSpec{
		Image: img, Workspace: t.TempDir(), Argv: probe,
		Network: sandbox.NetNone, Timeout: 5 * time.Minute, Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("blocked run: %v", err)
	}
	if blocked.ExitCode == 0 {
		t.Fatal("the sandbox reached the network under NetNone: this project's egress policy does not enforce what SANDBOX_NF_NETWORK_POLICY=true claims")
	}
}
