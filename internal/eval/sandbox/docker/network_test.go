package docker_test

import (
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/sandbox/docker"
)

func TestNetworkArgv_IsInternal(t *testing.T) {
	a := docker.NetworkArgv("whetstone-net-1")
	// --internal is the whole enforcement mechanism. Without it the run
	// container has a default route and the proxy is advisory: a skill that
	// ignores HTTPS_PROXY reaches anything it likes, and the allowlist becomes
	// a suggestion that reads like a control.
	if !has(a, "--internal") {
		t.Errorf("network is not internal; the allowlist would be advisory:\n%v", a)
	}
	if !has(a, "network", "create", "whetstone-net-1") {
		t.Errorf("argv = %v", a)
	}
}

func TestProxyArgv_AttachesTheProxyToTheInternalNetworkUnderItsAlias(t *testing.T) {
	a := docker.ProxyArgv("whetstone-net-1", "whetstone-proxy-1", "whetstone-base:1")
	for _, want := range []string{"--network", "whetstone-net-1", "--network-alias", "proxy", "--rm", "-d"} {
		if !has(a, want) {
			t.Errorf("argv missing %q:\n%v", want, a)
		}
	}
	// The proxy's own egress comes from a second attachment made after it
	// starts; it must not be given a bridge here, or the internal network's
	// members could route through the same interface.
	if strings.Contains(strings.Join(a, " "), "bridge") {
		t.Errorf("proxy created on the bridge network directly:\n%v", a)
	}
}
