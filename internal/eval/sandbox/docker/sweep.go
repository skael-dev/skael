package docker

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// SweepMinAge is how long an orphaned resource must have existed before Sweep
// removes it. A var so tests can shrink it.
var SweepMinAge = time.Minute

// Sweep removes whetstone-labeled containers and networks whose creating
// process is no longer alive. Call once per eval, before any run starts.
// A resource is removed only when its pid is confirmed dead AND it is older
// than SweepMinAge. A live pid is never touched regardless of age.
func (d *Driver) Sweep(ctx context.Context) {
	d.sweepContainers(ctx)
	d.sweepNetworks(ctx)
}

func (d *Driver) sweepContainers(ctx context.Context) {
	out, err := d.output(ctx, "ps", "-aq", "--filter", "label="+ownerLabelKey)
	if err != nil {
		d.o.Logger("docker: sweep: listing containers: %v", err)
		return
	}
	for _, id := range splitIDs(out) {
		if !d.orphaned(ctx, "inspect",
			"--format", "{{.Created}}|{{index .Config.Labels \""+pidLabelKey+"\"}}", id) {
			continue
		}
		if rmOut, err := d.output(ctx, "rm", "-f", id); err != nil {
			d.o.Logger("docker: sweep: removing container %s: %v\n%s", id, err, rmOut)
		}
	}
}

func (d *Driver) sweepNetworks(ctx context.Context) {
	out, err := d.output(ctx, "network", "ls", "-q", "--filter", "label="+ownerLabelKey)
	if err != nil {
		d.o.Logger("docker: sweep: listing networks: %v", err)
		return
	}
	for _, id := range splitIDs(out) {
		if !d.orphaned(ctx, "network", "inspect",
			"--format", "{{.Created}}|{{index .Labels \""+pidLabelKey+"\"}}", id) {
			continue
		}
		if rmOut, err := d.output(ctx, "network", "rm", id); err != nil {
			d.o.Logger("docker: sweep: removing network %s: %v\n%s", id, err, rmOut)
		}
	}
}

// createdLayouts: RFC3339Nano for containers, space-separated for networks.
var createdLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999 -0700 MST",
}

// orphaned reports whether a resource's creating pid is dead and the resource
// is older than SweepMinAge. Returns false when it cannot inspect or parse.
func (d *Driver) orphaned(ctx context.Context, inspectArgs ...string) bool {
	out, err := d.output(ctx, inspectArgs...)
	if err != nil {
		return false
	}
	created, rest, ok := strings.Cut(out, "|")
	if !ok {
		return false
	}
	var age time.Duration
	parsed := false
	for _, layout := range createdLayouts {
		if t, err := time.Parse(layout, created); err == nil {
			age = time.Since(t)
			parsed = true
			break
		}
	}
	if !parsed {
		d.o.Logger("docker: sweep: could not parse creation time %q in any known layout", created)
		return false
	}
	if age < SweepMinAge {
		return false
	}
	if pid, err := strconv.Atoi(rest); err == nil && pidAlive(pid) {
		return false
	}
	return true
}

// pidAlive errs toward alive: a permission error (pid reused by another user)
// is treated as alive rather than risk removing a resource whose owner cannot
// be proven dead.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrProcessDone) {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == syscall.ESRCH {
		return false
	}
	return true
}

func splitIDs(out string) []string {
	if out == "" {
		return nil
	}
	return strings.Fields(out)
}
