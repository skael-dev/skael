//go:build docker

package docker_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestSweep_RemovesOrphanedContainersAndNetworks simulates what a process
// killed by something the run's own context can't see (SIGKILL, a crash, or
// — before root.go grew a signal handler — a plain Ctrl-C) leaves behind: a
// still-running whetstone-run-* container, a still-running
// whetstone-proxy-* container, and the whetstone-net-* network connecting
// them. Sweep is what the CLI now runs before an eval starts to clear that
// out.
func TestSweep_RemovesOrphanedContainersAndNetworks(t *testing.T) {
	d := driver(t)
	ref := prepare(t, d)

	network := "whetstone-net-sweep-test"
	runName := "whetstone-run-sweep-test"
	proxyName := "whetstone-proxy-sweep-test"
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", runName, proxyName).Run()
		_ = exec.Command("docker", "network", "rm", network).Run()
	})

	if out, err := exec.Command("docker", "network", "create", network).CombinedOutput(); err != nil {
		t.Fatalf("docker network create: %v\n%s", err, out)
	}
	if out, err := exec.Command("docker", "run", "-d", "--name", proxyName, "--network", network,
		ref.Tag, "sleep", "300").CombinedOutput(); err != nil {
		t.Fatalf("docker run (orphaned proxy): %v\n%s", err, out)
	}
	if out, err := exec.Command("docker", "run", "-d", "--name", runName, "--network", network,
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

// TestSweep_LeavesUnrelatedContainersAlone is the "safe to run while another
// evaluation is in flight" half of Sweep's contract: anything that does not
// match the whetstone-run-/whetstone-proxy- prefixes must survive a sweep
// untouched.
func TestSweep_LeavesUnrelatedContainersAlone(t *testing.T) {
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
		t.Error("Sweep removed a container outside its own name prefixes")
	}
}
