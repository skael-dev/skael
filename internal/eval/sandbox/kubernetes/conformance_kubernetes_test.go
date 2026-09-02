//go:build kubernetes

package kubernetes_test

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

func clusterDriver(t *testing.T) sandbox.Driver {
	t.Helper()
	if os.Getenv("SANDBOX_K8S_NAMESPACE") == "" {
		t.Skip("no cluster configured")
	}
	d, err := resolve.FromEnv(os.Getenv).Build(t.Logf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return d
}

func TestKubernetesDriver_SatisfiesTheWorkspaceContract(t *testing.T) {
	sandboxtest.RunConformance(t, clusterDriver(t), sandbox.EnvSpec{Skill: "conformance"})
}

// The one test that needs a CNI which enforces NetworkPolicy. Without it, a
// restricted run is unrestricted and nothing else would notice.
func TestKubernetesDriver_ActuallyBlocksEgressUnderNetNone(t *testing.T) {
	d := clusterDriver(t)
	img, err := d.Prepare(context.Background(), sandbox.EnvSpec{Skill: "egress"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	const probe = "curl -sS -m 5 https://example.com"

	// Control: the same probe must succeed under NetFull, or a non-zero exit
	// under NetNone below proves nothing. Without this, the probe failing for
	// an unrelated reason — no DNS, a typo, a missing curl — reads exactly
	// like enforcement working, and the test goes green while enforcing
	// nothing.
	var controlErr strings.Builder
	control, err := d.Run(context.Background(), sandbox.RunSpec{
		Image:     img,
		Workspace: t.TempDir(),
		Argv:      []string{"sh", "-c", probe},
		Network:   sandbox.NetFull,
		Timeout:   2 * time.Minute,
		Stderr:    &controlErr,
	})
	if err != nil {
		t.Fatalf("control Run under NetFull: %v", err)
	}
	if control.ExitCode != 0 {
		t.Skipf("probe is broken, not the enforcement: %q exited %d under NetFull: %s", probe, control.ExitCode, controlErr.String())
	}

	var stderr strings.Builder
	res, err := d.Run(context.Background(), sandbox.RunSpec{
		Image:     img,
		Workspace: t.TempDir(),
		Argv:      []string{"sh", "-c", probe},
		Network:   sandbox.NetNone,
		Timeout:   2 * time.Minute,
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatal("the run reached the network under NetNone: this cluster's CNI does not enforce NetworkPolicy, and every restricted session here is unrestricted")
	}
}
