package gen_test

import (
	"strings"
	"testing"

	"github.com/skael-dev/skael/cli/whetstone/gen"
)

// TestSafeJoin_RefusesNestedTraversal exercises SafeJoin against traversal
// forms where ".." is not the first path segment.
func TestSafeJoin_RefusesNestedTraversal(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{
		"scripts/../../escape.sh",
		"./../escape.sh",
		"a/b/../../../escape.sh",
	} {
		if _, err := gen.SafeJoin(dir, rel); err == nil {
			t.Errorf("gen.SafeJoin(%q) = nil error, want an escape error", rel)
		}
	}
}

// naivePrefixCheck is the kind of check a less careful implementation might
// write: "reject it if it starts with '..'". It is included here only to be
// proven wrong, not as an alternative implementation.
func naivePrefixCheck(rel string) (safe bool) {
	return !strings.HasPrefix(rel, "..")
}

// TestNaivePrefixCheck_WouldMissNestedTraversal proves the easy mistake: a
// check that only inspects a path's prefix accepts "scripts/../../escape.sh"
// because the string does not start with "..", even though it resolves
// outside the bundle. SafeJoin does not make this mistake — it resolves the
// path with filepath.Join and re-derives it against dir with filepath.Rel,
// so a ".." buried after the first segment is still caught.
func TestNaivePrefixCheck_WouldMissNestedTraversal(t *testing.T) {
	evil := "scripts/../../escape.sh"

	if !naivePrefixCheck(evil) {
		t.Fatalf("test setup: expected naivePrefixCheck(%q) to (wrongly) allow it", evil)
	}

	dir := t.TempDir()
	if _, err := gen.SafeJoin(dir, evil); err == nil {
		t.Errorf("gen.SafeJoin(%q) = nil error, want an escape error — the naive prefix check "+
			"above wrongly allows this path, which is exactly what SafeJoin must catch instead", evil)
	}
}

func TestSafeJoin_RefusesAbsoluteAndEmpty(t *testing.T) {
	dir := t.TempDir()
	if _, err := gen.SafeJoin(dir, "/etc/passwd"); err == nil {
		t.Error("SafeJoin accepted an absolute path")
	}
	if _, err := gen.SafeJoin(dir, ""); err == nil {
		t.Error("SafeJoin accepted an empty path")
	}
}
