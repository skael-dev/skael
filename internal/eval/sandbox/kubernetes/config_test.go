package kubernetes

import (
	"errors"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

func validOptions() Options {
	return Options{Namespace: "skael-sandbox", Image: "ghcr.io/skael-dev/whetstone-base:1"}
}

func TestValidate_RequiresANamespaceAndAnImage(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*Options)
		want string
	}{
		{"no namespace", func(o *Options) { o.Namespace = "" }, "SANDBOX_K8S_NAMESPACE"},
		{"no image", func(o *Options) { o.Image = "" }, "SANDBOX_K8S_IMAGE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := validOptions()
			tc.mut(&o)
			err := o.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want an error naming %s", err, tc.want)
			}
		})
	}
}

// Without the operator's assertion the policy object may be accepted and
// silently ignored by the CNI, which would make a restricted run a lie.
func TestCheckNetwork_RefusesRestrictedPoliciesWithoutTheEnforcementAssertion(t *testing.T) {
	o := validOptions()
	for _, p := range []sandbox.NetworkPolicy{sandbox.NetNone, sandbox.NetAllowlist} {
		if err := o.CheckNetwork(p); !errors.Is(err, ErrNetworkPolicyUnenforced) {
			t.Errorf("CheckNetwork(%q) = %v, want ErrNetworkPolicyUnenforced", p, err)
		}
	}
	if err := o.CheckNetwork(sandbox.NetFull); err != nil {
		t.Errorf("CheckNetwork(full) = %v, want nil: an unrestricted run needs no enforcement", err)
	}
}

func TestCheckNetwork_AllowsEverythingOnceEnforcementIsAsserted(t *testing.T) {
	o := validOptions()
	o.NetworkPolicyEnforced = true
	for _, p := range []sandbox.NetworkPolicy{sandbox.NetNone, sandbox.NetAllowlist, sandbox.NetFull} {
		if err := o.CheckNetwork(p); err != nil {
			t.Errorf("CheckNetwork(%q) = %v, want nil", p, err)
		}
	}
}

// A runtime class alone is not a claim of isolation: the driver cannot tell
// kata from gvisor by its name.
func TestValidate_RefusesAnIsolationClaimWithoutARuntimeClass(t *testing.T) {
	o := validOptions()
	o.HardwareIsolated = true
	err := o.Validate()
	if err == nil || !strings.Contains(err.Error(), "SANDBOX_K8S_RUNTIME_CLASS") {
		t.Fatalf("Validate() = %v, want an error naming SANDBOX_K8S_RUNTIME_CLASS", err)
	}
}

// Malformed placement fails at start, not as a pod that never schedules.
func TestValidate_RefusesMalformedPlacement(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(*Options)
		want string
	}{
		{"tolerations", func(o *Options) { o.Tolerations = `{"key":"a"}` }, "SANDBOX_K8S_TOLERATIONS"},
		{"node selector", func(o *Options) { o.NodeSelector = `["agentpool"]` }, "SANDBOX_K8S_NODE_SELECTOR"},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := validOptions()
			c.set(&o)
			err := o.Validate()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Validate() = %v, want an error naming %s", err, c.want)
			}
		})
	}
}
