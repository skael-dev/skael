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
	var stderr strings.Builder
	res, err := d.Run(context.Background(), sandbox.RunSpec{
		Image:     img,
		Workspace: t.TempDir(),
		Argv:      []string{"sh", "-c", "curl -sS -m 5 https://example.com"},
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
