package runner

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStageRunWorkspace_CleansUpOnError pins the fix for every error path in
// stageRunWorkspace leaking the temp directory MkdirTemp had already
// created — suite.stageWorkspace, the identical function in the other
// package, already cleaned up on error; this one previously did not.
func TestStageRunWorkspace_CleansUpOnError(t *testing.T) {
	taskDir := t.TempDir()
	envDir := filepath.Join(taskDir, "environment")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(taskDir, "outside-target")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// copyTree refuses symlinks, so staging this environment always fails —
	// exercising the "staging environment" error return.
	if err := os.Symlink(target, filepath.Join(envDir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	before := tempDirs(t, "skael-run-*")

	// Empty root keeps this test asserting against os.TempDir(), which is
	// what tempDirs lists.
	_, err := stageRunWorkspace(taskDir, "")
	if err == nil {
		t.Fatal("stageRunWorkspace accepted an environment containing a symlink")
	}

	after := tempDirs(t, "skael-run-*")
	if len(after) != len(before) {
		t.Errorf("temp workspaces went %d -> %d; stageRunWorkspace leaked its directory on error", len(before), len(after))
	}
}

// tempDirs lists the entries of os.TempDir() matching pattern, so a test can
// assert a count of leaked directories rather than a specific path (the
// pattern includes a random suffix).
func tempDirs(t *testing.T, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), pattern))
	if err != nil {
		t.Fatalf("globbing temp dir: %v", err)
	}
	return matches
}
