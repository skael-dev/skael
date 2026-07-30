// Package docker implements the sandbox driver on the Docker CLI. It is the
// OSS default: self-hosted, own-team skills, and no infrastructure beyond a
// daemon that is already present. It is not sufficient isolation for untrusted
// code — containers share the host kernel — which is why sandbox.CheckPolicy
// refuses untrusted work here rather than this package deciding case by case.
//
// The CLI is shelled out to rather than using the Docker SDK: the SDK adds a
// large dependency tree and an API-version negotiation problem, and every
// operation needed here is one command whose flags are worth reading in full.
package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
)

// ErrDockerUnavailable is returned when no usable docker binary was found.
var ErrDockerUnavailable = errors.New("docker: no usable docker binary")

// execCommand is a substitution seam so build and Run are testable without
// actually shelling out.
var execCommand = exec.CommandContext

// Options configures the driver. The resource limits are per run: a 60-session
// tier at concurrency four must not be able to exhaust the host.
type Options struct {
	Binary    string
	BaseTag   string
	CPUs      string
	Memory    string
	PidsLimit int
	Logger    func(format string, args ...any)
}

// Driver is the Docker sandbox driver.
type Driver struct{ o Options }

// New resolves the docker binary and applies defaults.
func New(o Options) (*Driver, error) {
	if o.Binary == "" {
		bin, err := exec.LookPath("docker")
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrDockerUnavailable, err)
		}
		o.Binary = bin
	}
	if o.BaseTag == "" {
		o.BaseTag = imagespec.DefaultBaseTag
	}
	if o.CPUs == "" {
		o.CPUs = "2"
	}
	if o.Memory == "" {
		o.Memory = "4g"
	}
	if o.PidsLimit == 0 {
		o.PidsLimit = 512
	}
	if o.Logger == nil {
		o.Logger = func(string, ...any) {}
	}
	return &Driver{o: o}, nil
}

// Name identifies the driver in reports and diagnostics.
func (d *Driver) Name() string { return "docker" }

// HardwareIsolated reports false: runc shares the host kernel.
func (d *Driver) HardwareIsolated() bool { return false }

// Snapshot is a no-op. Docker has no checkpoint/restore path worth relying on,
// so a run rebuilds from the prepared layer, which is already cached.
func (d *Driver) Snapshot(context.Context, sandbox.ImageRef) (sandbox.SnapshotRef, error) {
	return sandbox.SnapshotRef{}, nil
}

// output runs a docker subcommand and returns its combined output, which is
// where docker writes its diagnostics.
func (d *Driver) output(ctx context.Context, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, d.o.Binary, args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return strings.TrimSpace(buf.String()), err
}
