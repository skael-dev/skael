// Package sandbox is the seam every evaluation run executes through. Nothing
// above this package knows whether the environment is a container or a fake.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/skael-dev/skael/internal/eval/spec"
)

var ErrDriverNotImplemented = errors.New("sandbox: driver not implemented")

// Driver runs commands in isolated environments.
type Driver interface {
	Name() string
	// HardwareIsolated gates untrusted work (shared-kernel drivers refuse it).
	HardwareIsolated() bool
	Prepare(ctx context.Context, e EnvSpec) (ImageRef, error)
	// Snapshot is optional: a zero SnapshotRef and no error means unsupported.
	Snapshot(ctx context.Context, r ImageRef) (SnapshotRef, error)
	Run(ctx context.Context, rs RunSpec) (RunResult, error)
}

// EnvSpec describes the environment a skill's runs need.
type EnvSpec struct {
	Skill   string
	Deps    spec.DepsDecl
	BaseTag string // empty = built-in default
}

// ImageRef identifies a prepared image.
type ImageRef struct {
	Tag        string
	DepsDigest string
}

// SnapshotRef identifies a restorable snapshot. Zero value means none.
type SnapshotRef struct{ ID string }

// NetworkPolicy is what a run may reach.
type NetworkPolicy string

const (
	NetNone      NetworkPolicy = "none"
	NetAllowlist NetworkPolicy = "allowlist"
	NetFull      NetworkPolicy = "full"
)

// Mount is a host path made visible inside a run.
type Mount struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

// RunSpec is one command in one environment.
type RunSpec struct {
	Image     ImageRef
	Snapshot  SnapshotRef
	// Workspace is an absolute local directory. The driver mirrors it into the
	// run before argv starts and mirrors it back afterwards. A bind-mounting
	// driver satisfies both directions at once; a remote driver copies. A
	// driver that fails to mirror back must return an error, never a short
	// result: a partial mirror is indistinguishable from a skill that produced
	// nothing.
	Workspace string
	WorkDir   string // container path; empty = DefaultWorkDir
	Argv      []string
	Env       []string
	Mounts    []Mount
	Network   NetworkPolicy
	Allow     []string // required for NetAllowlist, forbidden otherwise
	Timeout   time.Duration
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
}

// DefaultWorkDir is where a workspace appears inside a run.
const DefaultWorkDir = "/workspace"

// RunResult is what a finished run reports.
type RunResult struct {
	ExitCode int
	TimedOut bool
	// Cancelled distinguishes context cancellation from a timeout or a genuine
	// exit. A cancelled run must never be recorded as store.StatusFailed.
	Cancelled bool
	Duration  time.Duration
}

// Validate reports a RunSpec a driver should refuse. Every check guards a
// silent failure: a missing timeout hangs one run, an inconsistent allowlist
// looks restricted while being open.
func (rs RunSpec) Validate() error {
	if len(rs.Argv) == 0 {
		return errors.New("sandbox: run has no argv")
	}
	if rs.Image.Tag == "" {
		return errors.New("sandbox: run has no image")
	}
	if rs.Workspace == "" {
		return errors.New("sandbox: run has no workspace")
	}
	if !filepath.IsAbs(rs.Workspace) {
		return fmt.Errorf("sandbox: workspace %q must be an absolute host path", rs.Workspace)
	}
	if rs.Timeout <= 0 {
		return errors.New("sandbox: run has no timeout; one hung session stalls the whole eval")
	}
	switch rs.Network {
	case NetNone, NetFull:
		if len(rs.Allow) > 0 {
			return fmt.Errorf("sandbox: %d allow domains with network policy %q, which ignores them", len(rs.Allow), rs.Network)
		}
	case NetAllowlist:
		if len(rs.Allow) == 0 {
			return errors.New("sandbox: allowlist policy with an empty allowlist; declare the domains or use none")
		}
	default:
		return fmt.Errorf("sandbox: unknown network policy %q", rs.Network)
	}
	for _, m := range rs.Mounts {
		if !filepath.IsAbs(m.HostPath) {
			return fmt.Errorf("sandbox: mount host path %q must be absolute", m.HostPath)
		}
	}
	return nil
}
