// Package sprites is the hosted, hardware-isolated sandbox driver: Firecracker
// microVMs with checkpoint/restore, which is what makes a 60-session tier one
// environment build instead of sixty, and what makes untrusted skills
// evaluable at all.
//
// It is a stub. Every method returns sandbox.ErrDriverNotImplemented rather
// than approximating the behaviour, because an approximation that silently
// runs untrusted code on a shared kernel is the one outcome the interface
// exists to prevent. HardwareIsolated reports true so that the policy gate
// keeps its shape: when this driver lands, untrusted work starts working
// without a change anywhere above it.
package sprites

import (
	"context"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

// Driver is the Sprites sandbox driver.
type Driver struct{}

// New returns the stub driver.
func New() *Driver { return &Driver{} }

// Name identifies the driver in reports and diagnostics.
func (d *Driver) Name() string { return "sprites" }

// HardwareIsolated reports the isolation this driver will provide.
func (d *Driver) HardwareIsolated() bool { return true }

// Prepare is not implemented.
func (d *Driver) Prepare(context.Context, sandbox.EnvSpec) (sandbox.ImageRef, error) {
	return sandbox.ImageRef{}, sandbox.ErrDriverNotImplemented
}

// Snapshot is not implemented.
func (d *Driver) Snapshot(context.Context, sandbox.ImageRef) (sandbox.SnapshotRef, error) {
	return sandbox.SnapshotRef{}, sandbox.ErrDriverNotImplemented
}

// Run is not implemented.
func (d *Driver) Run(context.Context, sandbox.RunSpec) (sandbox.RunResult, error) {
	return sandbox.RunResult{}, sandbox.ErrDriverNotImplemented
}
