package docker

import (
	"crypto/rand"
	"encoding/hex"
)

// ownerLabelKey is the docker label every whetstone-created container and
// network carries: run containers (RunArgv), the allowlist proxy (ProxyArgv),
// and the private network it runs on (NetworkArgv). Sweep filters on this
// label rather than on a "whetstone-run-"/"whetstone-proxy-"/"whetstone-net-"
// name substring — a label is what lets Sweep ask docker directly ("is this
// mine") instead of trusting that every name-generating call site keeps
// matching a convention.
const ownerLabelKey = "whetstone.owner"

// ownerLabelValue identifies the process that created a resource — visible on
// `docker inspect` for anything this process creates. It is generated once
// per process, not read back by Sweep to decide what to remove: Sweep exists
// to reap orphans left by an *earlier* whetstone process, which by
// definition carries a different value here, not just this process's own
// leftovers. What actually protects a concurrently-running process's
// resources is sweepMinAge (sweep.go), not this value.
var ownerLabelValue = newOwnerLabelValue()

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

// ownerLabelArgs is the "--label" pair every docker create/run/network create
// call appends, so every whetstone-owned resource is discoverable by label
// without re-deriving it from a name.
func ownerLabelArgs() []string {
	return []string{"--label", OwnerLabel()}
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
