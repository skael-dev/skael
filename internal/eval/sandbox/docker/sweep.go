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

// SweepMinAge is how long an orphaned whetstone-labeled container or network
// must have existed before Sweep will remove it. A var, not a const, solely
// so a test can shrink it — production code has no legitimate reason to
// change it.
//
// The label alone cannot tell "an orphan left by a process that died" apart
// from "a network or proxy container another, currently-starting process is
// in the middle of creating": prepareAllowlist's own network-then-proxy
// sequence has a real window where the network exists with no endpoints yet,
// during which "docker network rm" would otherwise succeed against a
// concurrent process's not-yet-finished setup, and a freshly "docker run"
// container is trivially removable with "-f" regardless of who it belongs
// to. SweepMinAge is what keeps something that recent out of scope; anything
// that age or younger is left for a later Sweep call to pick up once it has
// actually gone stale. This is a secondary safety net, not the primary
// protection — see the pid check below for that.
var SweepMinAge = time.Minute

// Sweep removes containers and networks left behind by a run that never got
// to clean up after itself. docker.Driver.Run and prepareAllowlist only clean
// up under context cancellation — a process killed by something the context
// can't see (a SIGKILL, a host crash, or simply no signal handler installed
// at all, which whetstone did not have until this fix) leaves its run and
// proxy containers running and its private network in place. network.go's
// own comment names the consequence: a leaked network per run exhausts
// Docker's address pool inside one Deep tier. Nothing swept them before this.
//
// Call it once per eval, before any run starts.
//
// The invariant this guarantees: a live container or network belonging to
// another process is never removed. Every whetstone-created resource carries
// both ownerLabelKey (so Sweep never touches anything outside whetstone's
// own containers and networks) and pidLabelKey (the pid of the process that
// created it). Sweep only removes a labeled resource once both hold:
//
//  1. pidAlive(that pid) is false — the creating process is confirmed gone,
//     not merely old. This is what makes the guarantee a fact about the
//     resource's owner rather than a guess from its age: a session that
//     legitimately runs longer than SweepMinAge (any real agent session; the
//     default SessionTimeout is twenty minutes) still has a live pid, so it
//     is never touched, no matter how long it has been running or how many
//     other whetstone processes are sweeping concurrently.
//  2. the resource is older than SweepMinAge — a secondary guard against the
//     narrow window (described above) where a resource has just been created
//     and does not yet reflect its owner's final state, independent of pid
//     liveness.
//
// A resource with no parseable pid label (for instance one hand-labeled by a
// test, or one from a version of whetstone that predates the pid label) is
// treated as ownerless and falls back to the age check alone — the same
// behavior this function had before the pid check existed.
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

// createdLayouts are the timestamp formats docker's own ".Created" field has
// been observed to use: RFC3339Nano for a container, and a space-separated
// "date time offset zone-name" form (no "T", a bare zone abbreviation after
// the numeric offset) for a network. Both are tried because nothing in
// docker's docs promises one over the other, and orphaned is called for both
// resource kinds through the same code path.
var createdLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999 -0700 MST",
}

// orphaned runs a docker inspect variant (container or network) formatted to
// emit "<created>|<pid label>", and reports whether the resource is safe to
// remove: its owning pid must be confirmed dead (or absent/unparseable, in
// which case age is the only signal available) and it must be older than
// SweepMinAge. It returns false — leave it alone — when the resource could
// not be inspected at all (already removed by a concurrent sweep or by its
// owner finishing normally between the listing call and this one, not
// something Sweep should log as an error), when docker's own timestamp could
// not be parsed in any known layout, or when the owning pid is still alive.
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

// pidAlive reports whether pid names a currently-running process on this
// host, as best as a signal-0 probe can tell. It errs toward "alive": an
// error other than "no such process" (for instance a permission error,
// which would happen if the pid was reused by a process running as a
// different user) is treated as alive rather than risk removing a resource
// whose owner cannot be proven dead. This is the primary protection Sweep
// relies on to avoid ever removing a resource a live process still owns; the
// docker daemon and every whetstone process are assumed to run on the same
// host, which holds for this driver (it shells out to a local docker CLI).
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

// splitIDs splits docker's newline-delimited -q output (already
// whitespace-trimmed by d.output) into individual IDs, so a query with zero
// matches produces zero IDs rather than one empty one.
func splitIDs(out string) []string {
	if out == "" {
		return nil
	}
	return strings.Fields(out)
}
