// Package northflank implements the sandbox driver on Northflank's API. It
// exists so a worker can run where there is neither a Docker daemon nor a
// Kubernetes cluster: the worker holds an API token and nothing else.
//
// Northflank has no per-session egress control, so this driver cannot scope an
// allowlist to one run. It refuses what it cannot enforce instead.
package northflank

import (
	"errors"
	"fmt"
	"strings"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
)

var (
	// ErrNetworkPolicyUnenforced is returned for a restricted run when the
	// operator has not asserted that the project enforces egress.
	ErrNetworkPolicyUnenforced = errors.New("northflank: project egress enforcement is not asserted")
	// ErrDomainNotAllowed is returned when a run asks for a domain outside the
	// project's declared allowlist.
	ErrDomainNotAllowed = errors.New("northflank: domain outside the project allowlist")
)

// defaultPlan is the smallest plan that runs the base image comfortably.
const defaultPlan = "nf-compute-20"

// Options configures the driver. NetworkPolicyEnforced and HardwareIsolated
// are operator assertions about the project, not facts the driver can check.
type Options struct {
	Token              string
	Project            string
	Image              string
	RegistryCredential string
	Plan               string
	CLI                string

	// AllowedDomains is what the operator configured on the project. A run may
	// ask for a subset of it and nothing more.
	AllowedDomains []string

	NetworkPolicyEnforced bool
	HardwareIsolated      bool

	Logger func(format string, args ...any)
}

// Validate reports an unusable configuration. It never includes the token.
func (o Options) Validate() error {
	if o.Token == "" {
		return errors.New("northflank: no API token; set SANDBOX_NF_TOKEN")
	}
	if o.Project == "" {
		return errors.New("northflank: no project; set SANDBOX_NF_PROJECT to a project holding nothing but sandboxes")
	}
	return nil
}

// CheckNetwork refuses a run this driver cannot honour. Egress belongs to the
// project, so the only truthful answers are "the operator asserted it and this
// run fits inside it" or "no".
func (o Options) CheckNetwork(p sandbox.NetworkPolicy, allow []string) error {
	if p == sandbox.NetFull {
		return nil
	}
	if !o.NetworkPolicyEnforced {
		return fmt.Errorf("%w: policy %q needs an egress policy configured on project %s. Set SANDBOX_NF_NETWORK_POLICY=true once you have configured and confirmed it", ErrNetworkPolicyUnenforced, p, o.Project)
	}

	var uncovered []string
	for _, d := range allow {
		if !contains(o.AllowedDomains, d) {
			uncovered = append(uncovered, d)
		}
	}
	if len(uncovered) > 0 {
		return fmt.Errorf("%w: this run asks for %s, which SANDBOX_NF_ALLOWED_DOMAINS does not cover. Add the domains to the project's egress policy and to that variable, or run this skill on a driver that scopes egress per session",
			ErrDomainNotAllowed, strings.Join(uncovered, ", "))
	}
	return nil
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func (o Options) withDefaults() Options {
	if o.Image == "" {
		o.Image = imagespec.PublishedBaseImage
	}
	if o.Plan == "" {
		o.Plan = defaultPlan
	}
	if o.CLI == "" {
		o.CLI = "northflank"
	}
	if o.Logger == nil {
		o.Logger = func(string, ...any) {}
	}
	return o
}
