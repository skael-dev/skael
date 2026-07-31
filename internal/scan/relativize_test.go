package scan

import (
	"path/filepath"
	"testing"
)

// TestRelativize_StripsScanRoot pins the property that matters to a publisher:
// a finding names the file inside the bundle, not wherever the server happened
// to unpack it.
func TestRelativize_StripsScanRoot(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "tmp", "skael-publish-123")
	rep := &Report{Findings: []Finding{
		{File: filepath.Join(root, "SKILL.md")},
		{File: filepath.Join(root, "scripts", "deploy.sh")},
	}}

	Relativize(rep, root)

	want := []string{"SKILL.md", "scripts/deploy.sh"}
	for i, w := range want {
		if got := rep.Findings[i].File; got != w {
			t.Errorf("finding %d: got %q, want %q", i, got, w)
		}
	}
}

// TestRelativize_LeavesForeignPaths verifies that a path outside the scan root
// is left alone. An external scanner may report a path the server never
// unpacked; inventing a relationship to root would produce a "../.." path that
// is worse than the absolute one.
func TestRelativize_LeavesForeignPaths(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "tmp", "skael-publish-123")
	outside := filepath.Join(string(filepath.Separator), "etc", "passwd")
	rep := &Report{Findings: []Finding{{File: outside}}}

	Relativize(rep, root)

	if rep.Findings[0].File != outside {
		t.Errorf("got %q, want it left as %q", rep.Findings[0].File, outside)
	}
}

// TestRelativize_NilReport guards the call site: publish relativizes whatever
// ScanDir returned, and a nil report must not panic the publish route.
func TestRelativize_NilReport(t *testing.T) {
	Relativize(nil, "/tmp")
}
