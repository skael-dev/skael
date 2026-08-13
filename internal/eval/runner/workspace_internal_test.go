package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/internal/eval/suite"
)

// Every error path must remove the temp directory MkdirTemp already created,
// or a long eval leaks one workspace per failed session.
func TestStageEvalWorkspace_CleansUpOnError(t *testing.T) {
	suiteDir := t.TempDir()
	ev := suite.Eval{ID: 1, Prompt: "p", Files: []string{"evals/files/missing.csv"}}

	before := tempDirs(t, "skael-run-*")

	// Empty root keeps this test asserting against os.TempDir(), which is
	// what tempDirs lists.
	if _, err := stageEvalWorkspace(suiteDir, ev, ""); err == nil {
		t.Fatal("stageEvalWorkspace accepted an eval naming a file the set does not carry")
	}

	after := tempDirs(t, "skael-run-*")
	if len(after) != len(before) {
		t.Errorf("temp workspaces went %d -> %d; stageEvalWorkspace leaked its directory on error", len(before), len(after))
	}
}

// The expectations are the answer key, so nothing under evals/ may reach the
// workspace except the input files an eval names.
func TestStageEvalWorkspace_StagesOnlyTheNamedInputFiles(t *testing.T) {
	suiteDir := t.TempDir()
	filesDir := filepath.Join(suiteDir, "evals", "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filesDir, "in.csv"), []byte("a,b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(suiteDir, "evals", "evals.json"), []byte(`{"skill_name":"s"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ev := suite.Eval{ID: 1, Prompt: "p", Files: []string{"evals/files/in.csv"}}
	ws, err := stageEvalWorkspace(suiteDir, ev, "")
	if err != nil {
		t.Fatalf("stageEvalWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ws) })

	if _, err := os.Stat(filepath.Join(ws, "in.csv")); err != nil {
		t.Errorf("the named input file is not in the workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "evals")); err == nil {
		t.Error("the evals directory reached the workspace: the expectations are the answer key")
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
