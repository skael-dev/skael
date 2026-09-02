package northflank

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// defaultWaitInterval is how often the driver polls a service's rollout
// status while waiting for it to become runnable.
const defaultWaitInterval = 2 * time.Second

// Driver runs each session as a Northflank sandbox service.
type Driver struct {
	o            Options
	c            *client
	waitInterval time.Duration
}

// New validates the configuration and returns the driver.
func New(o Options) (*Driver, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	o = o.withDefaults()
	return &Driver{o: o, c: newClient(o), waitInterval: defaultWaitInterval}, nil
}

// Run arrives in a later task (workspace staging and execution); until then
// *Driver does not satisfy sandbox.Driver, so no interface assertion here.

func (d *Driver) Name() string { return "northflank" }

// HardwareIsolated reports the operator's assertion, never a guess.
// Northflank's own marketing claims VM-level isolation, but a vendor claim is
// not something this driver can verify from its API.
func (d *Driver) HardwareIsolated() bool { return d.o.HardwareIsolated }

// Snapshot is unsupported: a zero ref and no error.
func (d *Driver) Snapshot(context.Context, sandbox.ImageRef) (sandbox.SnapshotRef, error) {
	return sandbox.SnapshotRef{}, nil
}

// Prepare resolves the configured published image. There is no daemon here to
// build one, so a declared dependency is a refusal rather than a run on a
// base image that lacks it.
func (d *Driver) Prepare(_ context.Context, e sandbox.EnvSpec) (sandbox.ImageRef, error) {
	if declared := declaredDeps(e.Deps); len(declared) > 0 {
		return sandbox.ImageRef{}, fmt.Errorf(
			"northflank: skill %q declares dependencies this driver cannot install (%s). It resolves a published image and cannot build one; add them to the base image, or run this skill on the docker driver",
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
