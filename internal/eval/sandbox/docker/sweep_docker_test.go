//go:build docker

package docker_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox/docker"
)

// withZeroSweepMinAge temporarily disables Sweep's age protection so a test
// can create a resource and sweep it in the same breath, without waiting out
// docker.SweepMinAge for real. Production code has no legitimate reason to
// touch this var; only tests do.
func withZeroSweepMinAge(t *testing.T) {
	t.Helper()
	orig := docker.SweepMinAge
	docker.SweepMinAge = 0
	t.Cleanup(func() { docker.SweepMinAge = orig })
}

// TestSweep_RemovesOrphanedContainersAndNetworks simulates what a process
// killed by something the run's own context can't see (SIGKILL, a crash, or
// — before root.go grew a signal handler — a plain Ctrl-C) leaves behind: a
// still-running run container, a still-running proxy container, and the
// network connecting them, all carrying the docker label every
// whetstone-created resource carries. Sweep is what the CLI now runs before
// an eval starts to clear that out.
func TestSweep_RemovesOrphanedContainersAndNetworks(t *testing.T) {
	withZeroSweepMinAge(t)
	d := driver(t)
	ref := prepare(t, d)

	network := "whetstone-net-sweep-test"
	runName := "whetstone-run-sweep-test"
	proxyName := "whetstone-proxy-sweep-test"
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", runName, proxyName).Run()
		_ = exec.Command("docker", "network", "rm", network).Run()
	})

	label := docker.OwnerLabel()
	if out, err := exec.Command("docker", "network", "create", "--label", label, network).CombinedOutput(); err != nil {
		t.Fatalf("docker network create: %v\n%s", err, out)
	}
	if out, err := exec.Command("docker", "run", "-d", "--name", proxyName, "--label", label, "--network", network,
		ref.Tag, "sleep", "300").CombinedOutput(); err != nil {
		t.Fatalf("docker run (orphaned proxy): %v\n%s", err, out)
	}
	if out, err := exec.Command("docker", "run", "-d", "--name", runName, "--label", label, "--network", network,
		ref.Tag, "sleep", "300").CombinedOutput(); err != nil {
		t.Fatalf("docker run (orphaned run container): %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	d.Sweep(ctx)

	containers, err := exec.Command("docker", "ps", "-aq", "--filter", "name=whetstone-run-sweep-test",
		"--filter", "name=whetstone-proxy-sweep-test").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	if n := len(strings.Fields(string(containers))); n != 0 {
		t.Errorf("Sweep left %d orphaned containers behind", n)
	}

	networks, err := exec.Command("docker", "network", "ls", "-q", "--filter", "name="+network).Output()
	if err != nil {
		t.Fatalf("docker network ls: %v", err)
	}
	if n := len(strings.Fields(string(networks))); n != 0 {
		t.Errorf("Sweep left %d orphaned networks behind", n)
	}
}

// TestSweep_LeavesUnrelatedContainersAlone is one half of "safe to call
// while another evaluation is genuinely in flight": anything that does not
// carry whetstone's own docker label must survive a sweep untouched,
// regardless of age.
func TestSweep_LeavesUnrelatedContainersAlone(t *testing.T) {
	withZeroSweepMinAge(t)
	d := driver(t)
	ref := prepare(t, d)

	name := "not-a-whetstone-container-sweep-test"
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })

	if out, err := exec.Command("docker", "run", "-d", "--name", name, ref.Tag, "sleep", "300").CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	d.Sweep(ctx)

	out, err := exec.Command("docker", "ps", "-aq", "--filter", "name="+name).Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	if len(strings.Fields(string(out))) != 1 {
		t.Error("Sweep removed a container carrying no whetstone label")
	}
}

// TestSweep_LeavesRecentlyCreatedLabeledResourcesAlone is the other half:
// this is the exact shape of the finding that motivated the age check —
// docker.SweepMinAge left at its real (non-zeroed) default, a container and
// a network created moments ago, both correctly labeled as whetstone's own,
// simulating what a concurrently-*starting* whetstone process's still-being-
// constructed run or allowlist proxy looks like from another process's
// Sweep call. Neither may be removed: the label alone cannot tell that case
// apart from a genuine orphan, and only the age check can.
func TestSweep_LeavesRecentlyCreatedLabeledResourcesAlone(t *testing.T) {
	d := driver(t)
	ref := prepare(t, d)

	network := "whetstone-net-sweep-recent-test"
	name := "whetstone-run-sweep-recent-test"
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
		_ = exec.Command("docker", "network", "rm", network).Run()
	})

	label := docker.OwnerLabel()
	if out, err := exec.Command("docker", "network", "create", "--label", label, network).CombinedOutput(); err != nil {
		t.Fatalf("docker network create: %v\n%s", err, out)
	}
	if out, err := exec.Command("docker", "run", "-d", "--name", name, "--label", label, "--network", network,
		ref.Tag, "sleep", "300").CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	d.Sweep(ctx)

	containers, err := exec.Command("docker", "ps", "-aq", "--filter", "name="+name).Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	if len(strings.Fields(string(containers))) != 1 {
		t.Error("Sweep removed a labeled container younger than SweepMinAge")
	}

	networks, err := exec.Command("docker", "network", "ls", "-q", "--filter", "name="+network).Output()
	if err != nil {
		t.Fatalf("docker network ls: %v", err)
	}
	if len(strings.Fields(string(networks))) != 1 {
		t.Error("Sweep removed a labeled network younger than SweepMinAge")
	}
}
