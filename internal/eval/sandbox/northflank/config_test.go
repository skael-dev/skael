package northflank

import (
	"errors"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

func validOptions() Options {
	return Options{Token: "nf_test", Project: "skael-sandboxes"}
}

func TestValidate_RequiresACredentialAndAProject(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*Options)
		want string
	}{
		{"no token", func(o *Options) { o.Token = "" }, "SANDBOX_NF_TOKEN"},
		{"no project", func(o *Options) { o.Project = "" }, "SANDBOX_NF_PROJECT"},
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

// The token must never reach a log line or an error message.
func TestValidate_NeverEchoesTheToken(t *testing.T) {
	o := validOptions()
	o.Project = ""
	err := o.Validate()
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), o.Token) {
		t.Errorf("error text contains the API token: %q", err)
	}
}

// Without the operator's assertion the project's egress policy is unknown, so
// a restricted run must be refused rather than run wide open.
func TestCheckNetwork_RefusesRestrictedRunsWithoutTheEnforcementAssertion(t *testing.T) {
	o := validOptions()
	for _, p := range []sandbox.NetworkPolicy{sandbox.NetNone, sandbox.NetAllowlist} {
		if err := o.CheckNetwork(p, []string{"api.anthropic.com"}); !errors.Is(err, ErrNetworkPolicyUnenforced) {
			t.Errorf("CheckNetwork(%q) = %v, want ErrNetworkPolicyUnenforced", p, err)
		}
	}
	if err := o.CheckNetwork(sandbox.NetFull, nil); err != nil {
		t.Errorf("CheckNetwork(full) = %v, want nil: an unrestricted run needs no assertion", err)
	}
}

// This driver cannot set a per-session allowlist, so a run can only ask for
// what the operator has already configured on the project.
func TestCheckNetwork_RefusesADomainOutsideTheProjectAllowlist(t *testing.T) {
	o := validOptions()
	o.NetworkPolicyEnforced = true
	o.AllowedDomains = []string{"api.anthropic.com"}

	err := o.CheckNetwork(sandbox.NetAllowlist, []string{"api.anthropic.com", "evil.example.com"})
	if !errors.Is(err, ErrDomainNotAllowed) {
		t.Fatalf("CheckNetwork = %v, want ErrDomainNotAllowed", err)
	}
	if !strings.Contains(err.Error(), "evil.example.com") {
		t.Errorf("error %q must name the domain that is not covered", err)
	}
	if strings.Contains(err.Error(), "api.anthropic.com") {
		t.Errorf("error %q names a domain that IS covered, which buries the real one", err)
	}
}

func TestCheckNetwork_AcceptsASubsetOfTheProjectAllowlist(t *testing.T) {
	o := validOptions()
	o.NetworkPolicyEnforced = true
	o.AllowedDomains = []string{"api.anthropic.com", "pypi.org"}
	if err := o.CheckNetwork(sandbox.NetAllowlist, []string{"api.anthropic.com"}); err != nil {
		t.Errorf("CheckNetwork = %v, want nil for a subset", err)
	}
}

// NetNone asks for nothing, so it is inside every allowlist.
func TestCheckNetwork_AcceptsNetNoneWithNoAllowlistConfigured(t *testing.T) {
	o := validOptions()
	o.NetworkPolicyEnforced = true
	if err := o.CheckNetwork(sandbox.NetNone, nil); err != nil {
		t.Errorf("CheckNetwork(none) = %v, want nil", err)
	}
}

func TestWithDefaults_SuppliesTheShippedImageAndCLI(t *testing.T) {
	o := validOptions().withDefaults()
	if o.Image == "" || !strings.Contains(o.Image, "whetstone-base") {
		t.Errorf("Image = %q, want the published base", o.Image)
	}
	if o.CLI != "northflank" {
		t.Errorf("CLI = %q, want northflank", o.CLI)
	}
	if o.Plan == "" {
		t.Error("Plan must default; an unset plan fails at create time with a provider error")
	}
}
