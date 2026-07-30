// Package sandbox is the seam every evaluation run executes through. A run is
// one command in one isolated environment with one network policy; nothing
// above this package knows whether that environment is a container, a
// microVM, or a fake in a test.
//
// The interface exists for a security reason as much as a portability one:
// Docker shares the host kernel, so it is a legitimate driver for skills you
// generated yourself and not a legitimate driver for a skill someone sent you.
// CheckPolicy is where that distinction is enforced, once, rather than in each
// caller that might forget.
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

// ErrDriverNotImplemented is returned by a driver that conforms to the
// interface without implementing it.
var ErrDriverNotImplemented = errors.New("sandbox: driver not implemented")

// Driver runs commands in isolated environments.
type Driver interface {
	Name() string
	// HardwareIsolated reports whether an escape from a run is contained by
	// something stronger than a shared kernel. It gates untrusted work.
	HardwareIsolated() bool
	// Prepare builds or reuses the image layer a skill's runs execute in.
	Prepare(ctx context.Context, e EnvSpec) (ImageRef, error)
	// Snapshot captures a prepared image so a run can be restored from it
	// rather than rebuilt. Optional: a driver with no checkpoint support
	// returns a zero SnapshotRef and no error, and Run ignores it.
	Snapshot(ctx context.Context, r ImageRef) (SnapshotRef, error)
	Run(ctx context.Context, rs RunSpec) (RunResult, error)
}

// EnvSpec describes the environment a skill's runs need.
type EnvSpec struct {
	// Skill names the skill, for image tagging and diagnostics.
	Skill string
	// Deps are baked into a per-skill layer over the base image.
	Deps spec.DepsDecl
	// EnvFrag is a task-declared Dockerfile fragment. It is model-authored, so
	// it is validated rather than trusted — see imagespec.ValidateFragment.
	EnvFrag string
	// BaseTag is the base image to layer over. Empty means the built-in default.
	BaseTag string
}

// ImageRef identifies a prepared image.
type ImageRef struct {
	Tag        string
	DepsDigest string
}

// SnapshotRef identifies a restorable snapshot. The zero value means none.
type SnapshotRef struct{ ID string }

// NetworkPolicy is what a run may reach.
type NetworkPolicy string

const (
	// NetNone is the default: no route out at all. Every oracle and verifier
	// run uses it.
	NetNone NetworkPolicy = "none"
	// NetAllowlist permits exactly the declared domains and nothing else. Every
	// agent session uses it, because the agent CLI must reach its provider.
	NetAllowlist NetworkPolicy = "allowlist"
	// NetFull is unrestricted egress. Explicit opt-in, logged loudly.
	NetFull NetworkPolicy = "full"
)

// Mount is a host path made visible inside a run.
type Mount struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

// RunSpec is one command in one environment.
type RunSpec struct {
	Image    ImageRef
	Snapshot SnapshotRef
	// Workspace is the host directory mounted read-write at WorkDir. It is the
	// run's only writable surface and the only place artifacts are collected
	// from.
	Workspace string
	// WorkDir is the container path Workspace appears at. Empty means
	// DefaultWorkDir.
	WorkDir string
	Argv    []string
	Env     []string
	// Mounts are additional host paths — an adapter's auth directories, always
	// read-only.
	Mounts  []Mount
	Network NetworkPolicy
	// Allow lists permitted domains. Required when Network is NetAllowlist and
	// forbidden otherwise.
	Allow   []string
	Timeout time.Duration
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// DefaultWorkDir is where a workspace appears inside a run.
const DefaultWorkDir = "/workspace"

// RunResult is what a finished run reports. A non-zero ExitCode is a result,
// not an error: a verifier that fails is the measurement.
type RunResult struct {
	ExitCode int
	TimedOut bool
	Duration time.Duration
}

// Validate reports a RunSpec a driver should refuse. Each check exists because
// the failure it prevents is silent: a spec with no timeout hangs a 60-session
// eval on one run, and a network policy that ignores its own allowlist looks
// restricted while being open.
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
