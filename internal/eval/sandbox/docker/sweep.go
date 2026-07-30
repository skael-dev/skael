package docker

import (
	"context"
	"strings"
)

// Sweep removes containers and networks left behind by a run that never got
// to clean up after itself. docker.Driver.Run and prepareAllowlist only clean
// up under context cancellation — a process killed by something the context
// can't see (a SIGKILL, a host crash, or simply no signal handler installed
// at all, which whetstone did not have until this fix) leaves its
// "whetstone-run-*"/"whetstone-proxy-*" containers running and its
// "whetstone-net-*" network in place. network.go's own comment names the
// consequence: a leaked network per run exhausts Docker's address pool
// inside one Deep tier. Nothing swept them before this.
//
// Call it once per eval, before any run starts.
//
// Every removal failure — a container or network docker itself refuses to
// remove — is logged and otherwise ignored rather than treated as fatal, so
// Sweep degrades to a no-op on whatever it can't touch instead of aborting
// the eval it was meant to make safer to start. This is also what makes it
// safe to call while another evaluation is genuinely in flight on the same
// daemon: a network still holding an active endpoint fails to remove and is
// left alone. It does not, however, distinguish a stale container from one a
// concurrent evaluation still owns by any means other than that failure —
// two whetstone processes sweeping the same daemon at once is not something
// this guards beyond that.
func (d *Driver) Sweep(ctx context.Context) {
	for _, prefix := range []string{"whetstone-run-", "whetstone-proxy-"} {
		out, err := d.output(ctx, "ps", "-aq", "--filter", "name="+prefix)
		if err != nil {
			d.o.Logger("docker: sweep: listing containers matching %q: %v", prefix, err)
			continue
		}
		for _, id := range splitIDs(out) {
			if rmOut, err := d.output(ctx, "rm", "-f", id); err != nil {
				d.o.Logger("docker: sweep: removing container %s: %v\n%s", id, err, rmOut)
			}
		}
	}

	out, err := d.output(ctx, "network", "ls", "-q", "--filter", "name=whetstone-net-")
	if err != nil {
		d.o.Logger("docker: sweep: listing networks: %v", err)
		return
	}
	for _, id := range splitIDs(out) {
		if rmOut, err := d.output(ctx, "network", "rm", id); err != nil {
			d.o.Logger("docker: sweep: removing network %s: %v\n%s", id, err, rmOut)
		}
	}
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
