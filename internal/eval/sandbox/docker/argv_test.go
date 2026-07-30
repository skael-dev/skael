package docker_test

import (
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/docker"
)

func spec(mut ...func(*sandbox.RunSpec)) sandbox.RunSpec {
	rs := sandbox.RunSpec{
		Image:     sandbox.ImageRef{Tag: "whetstone-skill:abc"},
		Workspace: "/host/ws",
		Argv:      []string{"bash", "verifier/test.sh"},
		Network:   sandbox.NetNone,
		Timeout:   2 * time.Minute,
	}
	for _, f := range mut {
		f(&rs)
	}
	return rs
}

func argv(t *testing.T, rs sandbox.RunSpec, network string) []string {
	t.Helper()
	a, err := docker.RunArgv(rs, docker.Options{Binary: "docker", CPUs: "2", Memory: "4g", PidsLimit: 512}, "whetstone-run-1", network)
	if err != nil {
		t.Fatalf("RunArgv: %v", err)
	}
	return a
}

func has(a []string, want ...string) bool {
	joined := " " + strings.Join(a, " ") + " "
	for _, w := range want {
		if !strings.Contains(joined, " "+w+" ") {
			return false
		}
	}
	return true
}

func TestRunArgv_DropsPrivilegeByDefault(t *testing.T) {
	a := argv(t, spec(), "")
	// Every one of these has a specific failure it prevents. --rm and --name
	// are how a timed-out container is found and removed; the rest are what
	// keeps a misbehaving skill's blast radius inside the run. A container is
	// not a security boundary against a determined escape, which is what the
	// untrusted-mode gate is for — but running with the default capability set
	// gives away the isolation that is available.
	for _, want := range []string{
		"--rm", "--name", "whetstone-run-1",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", "512",
		"--cpus", "2",
		"--memory", "4g",
		"--network", "none",
		"--workdir", sandbox.DefaultWorkDir,
	} {
		if !has(a, want) {
			t.Errorf("argv missing %q:\n%v", want, a)
		}
	}
	if !has(a, "-v", "/host/ws:"+sandbox.DefaultWorkDir+":rw") {
		t.Errorf("workspace not mounted read-write:\n%v", a)
	}
	if a[len(a)-2] != "bash" || a[len(a)-1] != "verifier/test.sh" {
		t.Errorf("argv does not end with the command:\n%v", a)
	}
}

func TestRunArgv_MountsAuthDirectoriesReadOnly(t *testing.T) {
	a := argv(t, spec(func(rs *sandbox.RunSpec) {
		rs.Mounts = []sandbox.Mount{{HostPath: "/home/u/.claude", ContainerPath: "/home/runner/.claude", ReadOnly: true}}
	}), "")
	// Subscription credentials have to be visible for the agent to authenticate
	// and must not be writable: a skill that can edit them can persist across
	// runs.
	if !has(a, "-v", "/home/u/.claude:/home/runner/.claude:ro") {
		t.Errorf("auth mount is not read-only:\n%v", a)
	}
}

func TestRunArgv_RefusesAWritableMountOfAnAuthDirectory(t *testing.T) {
	_, err := docker.RunArgv(spec(func(rs *sandbox.RunSpec) {
		rs.Mounts = []sandbox.Mount{{HostPath: "/home/u/.claude", ContainerPath: "/home/runner/.claude"}}
	}), docker.Options{Binary: "docker"}, "n", "")
	if err == nil {
		t.Fatal("RunArgv accepted a writable extra mount")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("err = %v, want it to say extra mounts must be read-only", err)
	}
}

func TestRunArgv_AllowlistRoutesThroughTheProxyAndNothingElse(t *testing.T) {
	a := argv(t, spec(func(rs *sandbox.RunSpec) {
		rs.Network = sandbox.NetAllowlist
		rs.Allow = []string{"api.anthropic.com"}
	}), "whetstone-net-1")

	if !has(a, "--network", "whetstone-net-1") {
		t.Errorf("allowlist run not attached to its private network:\n%v", a)
	}
	// The proxy variables are the only egress path, and NO_PROXY must not
	// carve a hole in it. The network itself is --internal, so a process that
	// ignores the variables reaches nothing — enforcement is the topology, and
	// these variables are how a well-behaved client finds the one way out.
	if !has(a, "-e", "HTTPS_PROXY=http://proxy:8888", "-e", "HTTP_PROXY=http://proxy:8888") {
		t.Errorf("proxy environment missing:\n%v", a)
	}
	if strings.Contains(strings.Join(a, " "), "NO_PROXY=*") {
		t.Errorf("NO_PROXY wildcard bypasses the allowlist:\n%v", a)
	}
	if has(a, "--network", "none") || has(a, "--network", "bridge") {
		t.Errorf("allowlist run also carries another network:\n%v", a)
	}
}

func TestRunArgv_FullNetworkIsExplicit(t *testing.T) {
	a := argv(t, spec(func(rs *sandbox.RunSpec) { rs.Network = sandbox.NetFull }), "")
	if !has(a, "--network", "bridge") {
		t.Errorf("full policy did not select the bridge network:\n%v", a)
	}
}

func TestRunArgv_ValidatesTheSpec(t *testing.T) {
	_, err := docker.RunArgv(spec(func(rs *sandbox.RunSpec) { rs.Timeout = 0 }), docker.Options{Binary: "docker"}, "n", "")
	if err == nil {
		t.Fatal("RunArgv accepted a spec with no timeout")
	}
}
