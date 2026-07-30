package trajectory

import (
	"crypto/sha256"
	"encoding/hex"
)

// digestLen is how many hex characters of the hash to keep. 16 hex characters
// is 64 bits — ample for equality comparison and cache keys within a run,
// while keeping event files readable.
const digestLen = 16

// Digest returns a stable, non-reversible fingerprint of s, or "" for empty
// input so that callers can omit the field entirely. Digesting the empty string
// would produce a real-looking hash for a value that was never present.
func Digest(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])[:digestLen]
}
