//go:build docker

package docker_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox/docker"
)

// pidLabelKey mirrors the unexported label key sweep.go reads
// ("whetstone.owner.pid"); duplicated here because these tests need to set
// it directly rather than through the driver's own labeling path (which
// always stamps the test process's own pid, not an arbitrary one).
const pidLabelKey = "whetstone.owner.pid"

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

// deadPID is a pid value chosen to be confidently not running on this host,
// without the raciness of spawning and killing a real process (which could
// be reused by the kernel before the assertion runs). It is picked to
// exceed any pid a real OS process table will ever hand out: Linux's
// pid_max tops out at 4,194,304 even at its most permissive
// (/proc/sys/kernel/pid_max), and macOS's is far lower still, so a value an
// order of magnitude above the Linux ceiling is safe on both without
// depending on ephemeral process lifecycle.
const deadPID = 999999999

// TestSweep_RemovesAContainerWhosePidIsConfirmedDead pins the orphan-cleanup
// direction of the pid guard: a whetstone-labeled container whose recorded
// pid does not belong to any running process, and which is past
// SweepMinAge, is removed. This is the "cleanup still works" half of the
// pid check added alongside the age check.
func TestSweep_RemovesAContainerWhosePidIsConfirmedDead(t *testing.T) {
	withZeroSweepMinAge(t)
	d := driver(t)
	ref := prepare(t, d)

	name := "whetstone-run-sweep-deadpid-test"
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })

	pidLabel := fmt.Sprintf("%s=%d", pidLabelKey, deadPID)
	if out, err := exec.Command("docker", "run", "-d", "--name", name,
		"--label", docker.OwnerLabel(), "--label", pidLabel,
		ref.Tag, "sleep", "300").CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	d.Sweep(ctx)

	out, err := exec.Command("docker", "ps", "-aq", "--filter", "name="+name).Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	if n := len(strings.Fields(string(out))); n != 0 {
		t.Errorf("Sweep left %d containers with a confirmed-dead owner pid behind", n)
	}
}

// TestSweep_LeavesAContainerWithALivePidAlone is the direction that matters
// most: a whetstone-labeled container whose recorded pid names a genuinely
// running process (the test binary's own pid, guaranteed alive for the
// duration of this test) must survive a sweep even with the age guard
// zeroed out — this is what protects a running evaluation's containers from
// a concurrent Sweep call, independent of how long they've existed.
//
// This test is the one a mutation that deletes the pidAlive() call from
// orphaned would break: without it, only the (here-zeroed) age check gates
// removal, and this container would be removed.
func TestSweep_LeavesAContainerWithALivePidAlone(t *testing.T) {
	withZeroSweepMinAge(t)
	d := driver(t)
	ref := prepare(t, d)

	name := "whetstone-run-sweep-livepid-test"
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })

	pidLabel := fmt.Sprintf("%s=%d", pidLabelKey, os.Getpid())
	if out, err := exec.Command("docker", "run", "-d", "--name", name,
		"--label", docker.OwnerLabel(), "--label", pidLabel,
		ref.Tag, "sleep", "300").CombinedOutput(); err != nil {
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
		t.Error("Sweep removed a container whose owner pid is still alive")
	}
}
