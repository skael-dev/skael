package kubernetes

import (
	"regexp"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

func testNames(t *testing.T) names {
	t.Helper()
	n, err := newNames()
	if err != nil {
		t.Fatalf("newNames: %v", err)
	}
	return n
}

// enforcedOptions is validOptions with the operator's NetworkPolicy
// assertion set. config_test.go establishes that CheckNetwork refuses every
// restricted policy, NetNone included, without this assertion — a rendering
// test that only cares about pod shape needs the assertion set so it is not
// also exercising that refusal.
func enforcedOptions() Options {
	o := validOptions()
	o.NetworkPolicyEnforced = true
	return o
}

func baseSpec() sandbox.RunSpec {
	return sandbox.RunSpec{
		Image:     sandbox.ImageRef{Tag: "ghcr.io/skael-dev/whetstone-base:1"},
		Workspace: "/tmp/ws",
		Argv:      []string{"claude", "-p", "go"},
		Network:   sandbox.NetNone,
		Timeout:   10 * time.Minute,
	}
}

// A Job controller would retry a graded session and corrupt the score, and a
// missing deadline leaves a hung pod holding cluster capacity forever.
func TestSessionPod_NeverRestartsAndAlwaysCarriesADeadline(t *testing.T) {
	p, err := SessionPod(baseSpec(), enforcedOptions().withDefaults(), testNames(t))
	if err != nil {
		t.Fatalf("SessionPod: %v", err)
	}
	if p.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy = %q, want Never", p.Spec.RestartPolicy)
	}
	if p.Spec.ActiveDeadlineSeconds == nil || *p.Spec.ActiveDeadlineSeconds != 600 {
		t.Errorf("ActiveDeadlineSeconds = %v, want 600", p.Spec.ActiveDeadlineSeconds)
	}
}

// argv runs through exec, so the pod's own entrypoint must idle. A pod that
// started argv itself could not have its workspace staged first.
func TestSessionPod_IdlesRatherThanRunningArgv(t *testing.T) {
	rs := baseSpec()
	p, err := SessionPod(rs, enforcedOptions().withDefaults(), testNames(t))
	if err != nil {
		t.Fatalf("SessionPod: %v", err)
	}
	c := p.Spec.Containers[0]
	joined := strings.Join(append(c.Command, c.Args...), " ")
	for _, arg := range rs.Argv {
		if strings.Contains(joined, arg) {
			t.Fatalf("entrypoint %q contains argv %q; argv must run through exec", joined, arg)
		}
	}
	if len(c.Command) == 0 {
		t.Fatal("entrypoint is empty; the pod would run the image's own command")
	}
}

func TestSessionPod_DropsPrivilegeAndRunsAsTheImagesUser(t *testing.T) {
	p, err := SessionPod(baseSpec(), enforcedOptions().withDefaults(), testNames(t))
	if err != nil {
		t.Fatalf("SessionPod: %v", err)
	}
	sc := p.Spec.Containers[0].SecurityContext
	if sc == nil {
		t.Fatal("no SecurityContext")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Error("AllowPrivilegeEscalation must be false")
	}
	if sc.Capabilities == nil || len(sc.Capabilities.Drop) == 0 || sc.Capabilities.Drop[0] != "ALL" {
		t.Error("capabilities must drop ALL")
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != 1000 {
		t.Error("must run as uid 1000, the image's runner user")
	}
}

// Mounts are host paths and a pod has no host to mount from. Accepting them
// would produce an empty directory and a session that scores as a skill which
// did nothing.
func TestSessionPod_RefusesHostMounts(t *testing.T) {
	rs := baseSpec()
	rs.Mounts = []sandbox.Mount{{HostPath: "/home/u/.claude", ContainerPath: "/home/runner/.claude", ReadOnly: true}}
	_, err := SessionPod(rs, validOptions().withDefaults(), testNames(t))
	if err == nil || !strings.Contains(err.Error(), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("SessionPod = %v, want a refusal naming CLAUDE_CODE_OAUTH_TOKEN", err)
	}
}

func TestSessionPod_AppliesTheRuntimeClassWhenConfigured(t *testing.T) {
	o := enforcedOptions()
	o.RuntimeClass = "kata"
	p, err := SessionPod(baseSpec(), o.withDefaults(), testNames(t))
	if err != nil {
		t.Fatalf("SessionPod: %v", err)
	}
	if p.Spec.RuntimeClassName == nil || *p.Spec.RuntimeClassName != "kata" {
		t.Errorf("RuntimeClassName = %v, want kata", p.Spec.RuntimeClassName)
	}
}

func TestSessionPod_PointsTheAllowlistRunAtTheProxyPod(t *testing.T) {
	rs := baseSpec()
	rs.Network = sandbox.NetAllowlist
	rs.Allow = []string{"api.anthropic.com"}
	n := testNames(t)
	p, err := SessionPod(rs, enforcedOptions().withDefaults(), n)
	if err != nil {
		t.Fatalf("SessionPod: %v", err)
	}
	env := map[string]string{}
	for _, e := range p.Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	want := "http://" + n.Proxy + ":8888"
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
}

// The proxy shares no network namespace with the session, so a policy strict
// enough to force traffic through it does not also cut it off.
func TestEgressPolicy_AllowsOnlyTheProxyAndDNS(t *testing.T) {
	n := testNames(t)
	pol := EgressPolicy(validOptions().withDefaults(), n)
	if len(pol.Spec.Egress) != 2 {
		t.Fatalf("egress rules = %d, want 2 (proxy, DNS)", len(pol.Spec.Egress))
	}
	if pol.Spec.PodSelector.MatchLabels[roleLabelKey] != "session" {
		t.Errorf("policy must select the session pod, got %v", pol.Spec.PodSelector.MatchLabels)
	}
	var sawProxy bool
	for _, r := range pol.Spec.Egress {
		for _, to := range r.To {
			if to.PodSelector != nil && to.PodSelector.MatchLabels[roleLabelKey] == "proxy" {
				sawProxy = true
			}
		}
	}
	if !sawProxy {
		t.Error("no egress rule targets the proxy pod")
	}
}

func TestProxyConfigMap_CarriesBothRenderedHalves(t *testing.T) {
	cm, err := ProxyConfigMap([]string{"api.anthropic.com"}, validOptions().withDefaults(), testNames(t))
	if err != nil {
		t.Fatalf("ProxyConfigMap: %v", err)
	}
	conf, ok := cm.Data["tinyproxy.conf"]
	if !ok {
		t.Fatal("no tinyproxy.conf key")
	}
	filter, ok := cm.Data["filter"]
	if !ok {
		t.Fatal("no filter key")
	}
	if strings.Contains(conf, "api.anthropic.com") {
		t.Error("the allowlist belongs in the filter file, not the config")
	}
	// imagespec.ProxyConfig renders each entry as an anchored, QuoteMeta'd
	// regexp (see internal/eval/sandbox/imagespec/validate.go), so the filter
	// never carries the domain as a plain substring; it must be found by
	// matching, the same way imagespec's own test does.
	var matched bool
	for _, line := range strings.Split(strings.TrimSpace(filter), "\n") {
		re, err := regexp.Compile(strings.TrimSpace(line))
		if err != nil {
			t.Fatalf("filter entry %q did not compile as a regexp: %v", line, err)
		}
		if re.MatchString("api.anthropic.com") {
			matched = true
		}
	}
	if !matched {
		t.Errorf("filter does not carry the allowed domain: %q", filter)
	}
}
