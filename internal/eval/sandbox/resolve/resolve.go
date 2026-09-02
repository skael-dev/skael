// Package resolve is the one place the environment becomes a sandbox driver.
// Both cmd/skael-worker and cli/whetstone go through it, so one environment
// configures both and a misconfiguration is described in the same words by
// "whetstone doctor" and by the worker's startup log. This mirrors
// internal/eval/provider, which does the same job for the LLM backend.
package resolve

import (
	"context"
	"fmt"
	"os"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/docker"
	"github.com/skael-dev/skael/internal/eval/sandbox/kubernetes"
)

// Config is the resolved sandbox configuration. It is data only: nothing
// dials anything until Build is called.
type Config struct {
	Driver  string
	BaseTag string
	K8s     kubernetes.Options
}

// FromEnv reads the configuration. look is os.Getenv in production; tests
// pass a map lookup instead.
func FromEnv(look func(string) string) Config {
	if look == nil {
		look = os.Getenv
	}
	drv := look("SANDBOX_DRIVER")
	if drv == "" {
		drv = "docker"
	}
	return Config{
		Driver:  drv,
		BaseTag: look("WHETSTONE_BASE_TAG"),
		K8s: kubernetes.Options{
			Namespace:             look("SANDBOX_K8S_NAMESPACE"),
			Image:                 look("SANDBOX_K8S_IMAGE"),
			PullSecret:            look("SANDBOX_K8S_PULL_SECRET"),
			RuntimeClass:          look("SANDBOX_K8S_RUNTIME_CLASS"),
			HardwareIsolated:      look("SANDBOX_K8S_HARDWARE_ISOLATED") == "true",
			NetworkPolicyEnforced: look("SANDBOX_K8S_NETWORK_POLICY") == "true",
		},
	}
}

// Warnings describes a configuration that will run but is weaker than it
// looks. It never fails a build: an operator who has not asserted isolation
// or network enforcement gets a driver that refuses the runs that need them,
// not a silent one that pretends to provide them.
func (c Config) Warnings() []string {
	var w []string
	if c.Driver != "kubernetes" {
		return w
	}
	if !c.K8s.NetworkPolicyEnforced {
		w = append(w, "SANDBOX_K8S_NETWORK_POLICY is not set: this cluster's CNI enforcement is not asserted, so a run that restricts the network will be refused")
	}
	if !c.K8s.HardwareIsolated {
		w = append(w, "SANDBOX_K8S_HARDWARE_ISOLATED is not set: untrusted work will be refused, as on the docker driver")
	}
	return w
}

// Build returns the configured driver.
func (c Config) Build(logger func(format string, args ...any)) (sandbox.Driver, error) {
	switch c.Driver {
	case "docker":
		return docker.New(docker.Options{BaseTag: c.BaseTag, Logger: logger})
	case "kubernetes":
		o := c.K8s
		o.Logger = logger
		// NewInCluster, not a clientset built here, keeps the Kubernetes
		// client SDK out of every package but the kubernetes sandbox one.
		return kubernetes.NewInCluster(o)
	default:
		return nil, fmt.Errorf("sandbox: unknown SANDBOX_DRIVER %q; supported values are docker and kubernetes", c.Driver)
	}
}

// RequiresHostSharedRoots reports whether the driver bind-mounts host paths
// into sandboxes. Only the docker driver does, which is why a containerized
// worker needs WORKER_RUN_ROOT and a kubernetes one does not.
func RequiresHostSharedRoots(c Config) bool { return c.Driver == "docker" }

// Sweep removes resources a killed earlier run left behind, on a driver that
// has such a concept. The kubernetes driver resolves a published image and
// leaves nothing of its own to sweep.
func Sweep(ctx context.Context, d sandbox.Driver) {
	if s, ok := d.(interface{ Sweep(context.Context) }); ok {
		s.Sweep(ctx)
	}
}

// EnsureBase builds the base image, on a driver that builds one. A driver
// that resolves a published image instead has nothing to ensure.
func EnsureBase(ctx context.Context, d sandbox.Driver, slim bool) error {
	if e, ok := d.(interface {
		EnsureBase(context.Context, bool) error
	}); ok {
		return e.EnsureBase(ctx, slim)
	}
	return nil
}
