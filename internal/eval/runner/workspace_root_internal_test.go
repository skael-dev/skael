package runner

import (
	"os"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/suite"
)

// A containerized runner can only produce working sandboxes if its workspaces
// are created somewhere the Docker daemon resolves to the same directory. That
// is the whole purpose of Options.WorkspaceRoot, so it must actually decide
// where the workspace lands.
func TestStageEvalWorkspace_HonoursTheConfiguredRoot(t *testing.T) {
	suiteDir := t.TempDir()
	ev := suite.Eval{ID: 1, Prompt: "do the thing"}

	root := t.TempDir()
	ws, err := stageEvalWorkspace(suiteDir, ev, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ws) })

	if !strings.HasPrefix(ws, root) {
		t.Fatalf("workspace %s was not created under the configured root %s; a containerized "+
			"worker would bind-mount a path the host daemon cannot resolve", ws, root)
	}
}

// An empty root must keep meaning os.TempDir(), so the interactive CLI and
// every existing caller are unaffected.
func TestStageEvalWorkspace_EmptyRootStillUsesTempDir(t *testing.T) {
	ws, err := stageEvalWorkspace(t.TempDir(), suite.Eval{ID: 1, Prompt: "p"}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ws) })

	if !strings.HasPrefix(ws, os.TempDir()) {
		t.Fatalf("workspace %s is not under os.TempDir() %s", ws, os.TempDir())
	}
}
