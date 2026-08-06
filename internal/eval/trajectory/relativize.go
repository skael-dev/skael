package trajectory

import (
	"path"
	"strings"
)

// Relativize returns events whose Paths are expressed relative to root. An
// agent CLI reports absolute paths inside the sandbox; contract.MatchPath
// compares workspace-relative patterns and refuses an absolute candidate.
//
// A path not under root is left alone rather than forced: rewriting a genuine
// escape (a write to /etc/passwd) could turn a finding into a match against an
// innocent pattern. Left absolute it stays unevaluable and visible.
//
// The input is not mutated — these events are also persisted and replayed on
// resume, so relativising for scoring must not change what another caller
// writes.
func Relativize(events []Event, root string) []Event {
	if root == "" || len(events) == 0 {
		return events
	}
	// "/"-terminated so a sibling sharing the textual prefix
	// ("/workspace-other") is not treated as inside it.
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
