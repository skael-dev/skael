package docker

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
)

// ownerLabelKey is the docker label every whetstone-created container and
// network carries: run containers (RunArgv), the allowlist proxy (ProxyArgv),
// and the private network it runs on (NetworkArgv). Sweep filters on this
// label rather than on a "whetstone-run-"/"whetstone-proxy-"/"whetstone-net-"
// name substring — a label is what lets Sweep ask docker directly ("is this
// mine") instead of trusting that every name-generating call site keeps
// matching a convention.
const ownerLabelKey = "whetstone.owner"

// pidLabelKey is a second label, carrying the OS pid of the process whose
// Driver created the resource. This is what lets Sweep tell "an orphan left
// by a process that is gone" apart from "a resource that belongs to a
// different, still-running whetstone process" without relying on age alone:
// a pid that is still alive can never be a false negative the way a purely
// time-based guard can (a legitimate session can easily outlive
// SweepMinAge). See sweep.go's pidAlive.
const pidLabelKey = "whetstone.owner.pid"

// ownerLabelValue identifies the process that created a resource — visible on
// `docker inspect` for anything this process creates. It is generated once
// per process and, together with pidLabelKey, is what OwnerLabel()-scoped
// callers (this package's own tests) and Sweep use to reason about which
// process a resource belongs to.
var ownerLabelValue = newOwnerLabelValue()

// ownerPID is this process's OS pid, recorded once at package init and
// stamped onto every resource this process's Driver instances create.
var ownerPID = os.Getpid()

func newOwnerLabelValue() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing here is unreachable in practice; a constant
		// fallback keeps this a labeling nicety rather than something that
		// can panic package init.
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// ownerLabelArgs is the "--label" pairs every docker create/run/network
// create call appends, so every whetstone-owned resource is discoverable by
// label without re-deriving it from a name, and carries the creating
// process's pid so Sweep can tell whether that process is still alive.
func ownerLabelArgs() []string {
	return []string{
		"--label", OwnerLabel(),
		"--label", fmt.Sprintf("%s=%d", pidLabelKey, ownerPID),
	}
}

// OwnerLabel is the "key=value" docker label filter matching every container
// and network this process's Driver instances create — e.g. for
// "docker ps --filter label=<OwnerLabel()>". Exported so a caller (this
// package's own tests, notably) can scope a query to just this process's
// resources: two docker-tagged test binaries running concurrently against
// the same daemon get different values here, so counting by OwnerLabel()
// does not race the other binary's containers the way counting by a shared
// name prefix or the bare ownerLabelKey would.
func OwnerLabel() string { return ownerLabelKey + "=" + ownerLabelValue }
