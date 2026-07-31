package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
)

// TestAuthMounts_RewritesHomeAndDropsMissingEntries pins the fix for the
// defect where auth directories were mounted at their host path on both
// sides: the container's HOME is imagespec.ContainerHome, not the host's, so
// a "~/..." entry must resolve differently for HostPath (the host's own
// home) and ContainerPath (the image's "runner" home) — mounting the host
// path verbatim on both sides put every credential where the container-side
// CLI never looks, so no session could ever authenticate.
func TestAuthMounts_RewritesHomeAndDropsMissingEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("staging ~/.claude: %v", err)
	}

	var logs []string
	logf := func(format string, args ...any) { logs = append(logs, format) }

	mounts, err := authMounts([]string{"~/.claude", "~/.config/claude"}, logf)
	if err != nil {
		t.Fatalf("authMounts: %v", err)
	}

	// The non-existent "~/.config/claude" entry must be dropped, not passed
	// through as a bind-mount source that would error the run or have Docker
	// silently create a root-owned directory on the host.
	if len(mounts) != 1 {
		t.Fatalf("mounts = %+v, want exactly the one existing entry", mounts)
	}
	if len(logs) != 1 {
		t.Errorf("expected the missing auth dir to be logged as skipped, got %d log lines", len(logs))
	}

	m := mounts[0]
	if m.HostPath != claudeDir {
		t.Errorf("HostPath = %q, want %q (the host's own home)", m.HostPath, claudeDir)
	}
	wantContainer := filepath.Join(imagespec.ContainerHome, ".claude")
	if m.ContainerPath != wantContainer {
		t.Errorf("ContainerPath = %q, want %q (the image's runner home)", m.ContainerPath, wantContainer)
	}
	if !m.ReadOnly {
		t.Error("auth mount must be read-only")
	}
}
