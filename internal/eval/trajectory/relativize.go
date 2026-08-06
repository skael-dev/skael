package trajectory

import (
	"path"
	"strings"
)

// Relativize returns events whose Paths are expressed relative to root,
// leaving any path that is not under root exactly as it was.
//
// This exists because the two halves of the system speak different path
// languages and neither can be changed. An agent CLI reports what it actually
// touched, which is an absolute path inside the sandbox container
// ("/workspace/environment/docs/sdd.md"), while contract.MatchPath compares
// workspace-relative patterns and refuses an absolute candidate outright —
// deliberately, since an absolute candidate means something upstream lost the
// workspace root. Nothing sat between them, so every path-bearing contract
// rule came back unevaluable.
//
// That failure was not merely noisy, which is why this is worth a package-level
// helper rather than an inline strings.TrimPrefix. It pushed the drift score in
// both directions at once: step coverage, checkpoints and focus all collapsed
// to zero because nothing matched, while the violation and order components
// went vacuously *perfect* because nothing could be violated or mis-ordered
// either. The resulting adherence was a constant that read as a measurement.
//
// A path that is not under root is left alone rather than forced. Inventing a
// relationship that isn't there would let a genuine escape — a write to
// /etc/passwd — be rewritten into something that matches an innocent pattern,
// turning a finding into a pass. Left absolute, it stays unevaluable and
// visible, which is the outcome the contract package intends. The same
// "leave it alone" rule is why internal/scan.Relativize skips paths outside
// its root.
//
// The input is not mutated: the same events are persisted to events.jsonl and
// replayed on resume, so relativising for scoring must not change what another
// caller is about to write.
func Relativize(events []Event, root string) []Event {
	if root == "" || len(events) == 0 {
		return events
	}
	// Compare against a "/"-terminated root so a sibling directory that merely
	// shares the textual prefix ("/workspace-other") is not treated as being
	// inside it.
	prefix := path.Clean(root) + "/"

	out := make([]Event, len(events))
	copy(out, events)
	for i := range out {
		if len(out[i].Paths) == 0 {
			continue
		}
		paths := make([]string, len(out[i].Paths))
		copy(paths, out[i].Paths)
		for j, p := range paths {
			if !strings.HasPrefix(p, prefix) {
				continue
			}
			rel := strings.TrimPrefix(p, prefix)
			if rel == "" {
				continue
			}
			paths[j] = rel
		}
		out[i].Paths = paths
	}
	return out
}
