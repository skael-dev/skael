package docker

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
)

// ownerLabelKey is the docker label every whetstone-created resource carries.
// Sweep filters on it rather than on a name-prefix convention.
const ownerLabelKey = "whetstone.owner"

// pidLabelKey carries the creating process's OS pid, so Sweep can distinguish
// an orphan from a resource that belongs to a still-running process.
const pidLabelKey = "whetstone.owner.pid"

var ownerLabelValue = newOwnerLabelValue()
var ownerPID = os.Getpid()

func newOwnerLabelValue() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

func ownerLabelArgs() []string {
	return []string{
		"--label", OwnerLabel(),
		"--label", fmt.Sprintf("%s=%d", pidLabelKey, ownerPID),
	}
}

// OwnerLabel is the "key=value" filter matching this process's resources.
// Per-process so concurrent test binaries do not race each other.
func OwnerLabel() string { return ownerLabelKey + "=" + ownerLabelValue }
