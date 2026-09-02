package kubernetes

import (
	"context"
	"fmt"
	"strings"

	k8s "k8s.io/client-go/kubernetes"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// Driver runs each session as a pod.
type Driver struct {
	o  Options
	cs k8s.Interface
}

// New validates the configuration and returns the driver.
func New(o Options, cs k8s.Interface) (*Driver, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	return &Driver{o: o.withDefaults(), cs: cs}, nil
}

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
