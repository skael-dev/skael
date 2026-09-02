package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// Driver runs each session as a pod.
type Driver struct {
	o            Options
	cs           k8s.Interface
	ex           execer
	waitInterval time.Duration
}

// New validates the configuration and returns the driver.
func New(o Options, cs k8s.Interface, ex execer) (*Driver, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	if ex == nil {
		return nil, errors.New("kubernetes: no execer; the driver stages the workspace and runs argv through the exec subresource")
	}
	return &Driver{o: o.withDefaults(), cs: cs, ex: ex, waitInterval: defaultWaitInterval}, nil
}

// NewInCluster builds a driver from real cluster credentials. It is the only
// constructor client construction lives behind: a later task needs to build
// this driver from a package that must not import k8s.io/*, and an exported
// constructor returning an unexported execer would be unusable there anyway.
func NewInCluster(o Options) (*Driver, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	cfg, err := restConfig()
	if err != nil {
		return nil, fmt.Errorf("kubernetes: building the rest config: %w", err)
	}
	cs, err := k8s.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: building the clientset: %w", err)
	}
	return New(o, cs, &apiExecer{cs: cs, cfg: cfg, namespace: o.Namespace})
}

// restConfig prefers in-cluster credentials, which is where the worker runs
// in production, and falls back to a kubeconfig for local development.
func restConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
}

var _ sandbox.Driver = (*Driver)(nil)

func (d *Driver) Name() string { return "kubernetes" }

// HardwareIsolated reports the operator's assertion, never a guess. A runtime
// class name does not distinguish a microVM runtime from a shared-kernel one.
func (d *Driver) HardwareIsolated() bool { return d.o.HardwareIsolated }

// Snapshot is unsupported: a zero ref and no error.
func (d *Driver) Snapshot(context.Context, sandbox.ImageRef) (sandbox.SnapshotRef, error) {
	return sandbox.SnapshotRef{}, nil
}

// Prepare resolves the configured image. It cannot build one, so a declared
// dependency is a refusal rather than a run on a base that lacks it.
func (d *Driver) Prepare(_ context.Context, e sandbox.EnvSpec) (sandbox.ImageRef, error) {
	if declared := declaredDeps(e.Deps); len(declared) > 0 {
		return sandbox.ImageRef{}, fmt.Errorf(
			"kubernetes: skill %q declares dependencies this driver cannot install (%s). It resolves a published image and cannot build one; add them to the base image, or run this skill on the docker driver",
			e.Skill, strings.Join(declared, ", "))
	}
	if e.BaseTag == "" {
		e.BaseTag = imagespec.DefaultBaseTag
	}
	digest, err := imagespec.DepsDigest(e)
	if err != nil {
		return sandbox.ImageRef{}, err
	}
	return sandbox.ImageRef{Tag: d.o.Image, DepsDigest: digest}, nil
}

func declaredDeps(d spec.DepsDecl) []string {
	var out []string
	for _, g := range []struct {
		name string
		vals []string
	}{{"apt", d.Apt}, {"pip", d.Pip}, {"npm", d.Npm}} {
		for _, v := range g.vals {
			out = append(out, g.name+" "+v)
		}
	}
	return out
}
