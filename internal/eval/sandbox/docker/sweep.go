package docker

import (
	"context"
	"strings"
	"time"
)

// SweepMinAge is how long a whetstone-labeled container or network must have
// existed before Sweep will touch it. A var, not a const, solely so a test
// can shrink it — production code has no legitimate reason to change it.
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
// actually gone stale.
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
// What this actually guarantees: Sweep only ever acts on a resource carrying
// ownerLabelKey (so it never touches anything outside whetstone's own
// containers and networks) and only once that resource is older than
// SweepMinAge. That age check is a necessary, not sufficient, protection
// against removing a resource a concurrently-running evaluation still
// owns — a session that legitimately runs longer than SweepMinAge (any real
// agent session; the default SessionTimeout is twenty minutes) looks
// identical, by age alone, to an orphan from a crashed process, and Sweep
// cannot tell the two apart. Do not call Sweep from anywhere that might run
// concurrently with a long-lived session unless that risk is accepted; the
// one call site today (suite check, which only runs the oracle gate) is
// short-lived enough that the risk window is the CLI startup itself, not a
// whole eval.
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
		age, ok := d.resourceAge(ctx, "inspect", "--format", "{{.Created}}", id)
		if !ok || age < SweepMinAge {
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
		age, ok := d.resourceAge(ctx, "network", "inspect", "--format", "{{.Created}}", id)
		if !ok || age < SweepMinAge {
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
// docker's docs promises one over the other, and resourceAge is called for
// both resource kinds through the same code path.
var createdLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999 -0700 MST",
}

// resourceAge runs a docker inspect variant (container or network) formatted
// to emit only the resource's creation time, and returns how long ago that
// was. ok is false when the resource could not be inspected at all (already
// removed by a concurrent sweep or by its owner finishing normally between
// the listing call and this one — not something Sweep should log as an
// error) or when docker's own timestamp could not be parsed in any known
// layout.
func (d *Driver) resourceAge(ctx context.Context, inspectArgs ...string) (time.Duration, bool) {
	out, err := d.output(ctx, inspectArgs...)
	if err != nil {
		return 0, false
	}
	for _, layout := range createdLayouts {
		if created, err := time.Parse(layout, out); err == nil {
			return time.Since(created), true
		}
	}
	d.o.Logger("docker: sweep: could not parse creation time %q in any known layout", out)
	return 0, false
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
