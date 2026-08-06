package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A containerized runner can only produce working sandboxes if its workspaces
// are created somewhere the Docker daemon resolves to the same directory. That
// is the whole purpose of Options.WorkspaceRoot, so it must actually decide
// where the workspace lands.
func TestStageRunWorkspace_HonoursTheConfiguredRoot(t *testing.T) {
	taskDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(taskDir, "task.md"), []byte("do the thing"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	ws, err := stageRunWorkspace(taskDir, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ws) })

	if !strings.HasPrefix(ws, root) {
		t.Fatalf("workspace %s was not created under the configured root %s; a containerized "+
			"worker would bind-mount a path the host daemon cannot resolve", ws, root)
	}
	if _, err := os.Stat(filepath.Join(ws, "task.md")); err != nil {
		t.Fatalf("workspace under a configured root is not staged: %v", err)
	}
}

// An empty root must keep meaning os.TempDir(), so the interactive CLI and
// every existing caller are unaffected.
func TestStageRunWorkspace_EmptyRootStillUsesTempDir(t *testing.T) {
	taskDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(taskDir, "task.md"), []byte("do the thing"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := stageRunWorkspace(taskDir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ws) })

	if !strings.HasPrefix(ws, os.TempDir()) {
		t.Fatalf("workspace %s is not under os.TempDir() %s", ws, os.TempDir())
	}
}
