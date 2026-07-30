//go:build docker

package docker_test

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

func TestAllowlist_PermitsListedHostsAndRefusesEverythingElse(t *testing.T) {
	d := driver(t)
	ref := prepare(t, d)

	// A 401 from the API proves reachability without a key. Anything else —
	// a DNS failure, a proxy refusal — proves the opposite, which is what the
	// denied case asserts below.
	//
	// -X POST: verified directly against the live endpoint while building this
	// test, a GET to /v1/messages returns 405 Method Not Allowed before any
	// auth check runs, which would prove reachability just as well but not as
	// the specific code this test asserts on; POST is what actually reaches
	// the code path that returns 401 for a missing key.
	res, out := run(t, d, sandbox.RunSpec{
		Image: ref, Workspace: t.TempDir(), Timeout: 2 * time.Minute,
		Network: sandbox.NetAllowlist, Allow: []string{"api.anthropic.com"},
		Argv: []string{"sh", "-c", `curl -s -o /dev/null -w '%{http_code}' -X POST https://api.anthropic.com/v1/messages`},
	})
	if res.ExitCode != 0 || !strings.Contains(out, "401") {
		t.Errorf("allowlisted host unreachable: exit=%d out=%q", res.ExitCode, out)
	}

	res, out = run(t, d, sandbox.RunSpec{
		Image: ref, Workspace: t.TempDir(), Timeout: 2 * time.Minute,
		Network: sandbox.NetAllowlist, Allow: []string{"api.anthropic.com"},
		Argv: []string{"sh", "-c", `curl -s -f https://example.com`},
	})
	if res.ExitCode == 0 {
		t.Errorf("an unlisted host was reachable through the proxy: out=%q", out)
	}
}

func TestAllowlist_RefusesDirectEgressThatIgnoresTheProxy(t *testing.T) {
	d := driver(t)
	ref := prepare(t, d)

	// The proxy variables are a convention a payload can ignore. --internal is
	// what makes ignoring them useless, and this is the test that would catch
	// its removal — the previous test would still pass without it.
	res, out := run(t, d, sandbox.RunSpec{
		Image: ref, Workspace: t.TempDir(), Timeout: 2 * time.Minute,
		Network: sandbox.NetAllowlist, Allow: []string{"api.anthropic.com"},
		Argv: []string{"sh", "-c", `curl -s -f --noproxy '*' https://api.anthropic.com/v1/messages`},
	})
	if res.ExitCode == 0 {
		t.Errorf("direct egress succeeded with the proxy bypassed: out=%q", out)
	}
}

func TestAllowlist_RemovesItsNetworkAndProxy(t *testing.T) {
	d := driver(t)
	ref := prepare(t, d)
	_, _ = run(t, d, sandbox.RunSpec{
		Image: ref, Workspace: t.TempDir(), Timeout: time.Minute,
		Network: sandbox.NetAllowlist, Allow: []string{"api.anthropic.com"},
		Argv: []string{"true"},
	})
	// One leaked user-defined network per run exhausts Docker's address pool
	// well before a Deep tier finishes, and the failure it produces then is
	// "could not create network", which reads as nothing to do with the eval.
	out, err := exec.Command("docker", "network", "ls", "--filter", "name=whetstone-net-", "-q").Output()
	if err != nil {
		t.Fatalf("docker network ls: %v", err)
	}
	if n := len(strings.Fields(string(out))); n != 0 {
		t.Errorf("leaked %d networks", n)
	}

	proxies, err := exec.Command("docker", "ps", "-a", "--filter", "name=whetstone-proxy-", "-q").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	if n := len(strings.Fields(string(proxies))); n != 0 {
		t.Errorf("leaked %d proxy containers", n)
	}
}
