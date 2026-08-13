package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildFilesStampTheVersion guards a defect that cost a full paid eval
// before it was noticed. main.version defaults to "dev", and
// internal/evalqueue/routes.go refuses a report carrying "dev" or "" — so a
// worker built without -X main.version runs an entire tier, spends the tokens,
// scores it, and is rejected at the last step. Nothing else catches this: the
// binary compiles, every unit test passes, and the failure appears only after
// a real run against a real server.
//
// Asserting on file contents is blunt, and it is deliberate. The alternative
// is building the image in CI, which is far more expensive than this, and the
// thing being protected is a one-line flag that is easy to drop during an
// unrelated edit to either file.
func TestBuildFilesStampTheVersion(t *testing.T) {
	root := repoRoot(t)

	for _, f := range []string{"Dockerfile.worker", "justfile"} {
		t.Run(f, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(root, f))
			if err != nil {
				t.Fatalf("reading %s: %v", f, err)
			}
			if !strings.Contains(string(b), "-X main.version=") {
				t.Errorf("%s builds skael-worker without -X main.version=; the server rejects "+
					"a report from a worker reporting the default \"dev\", so every eval it runs "+
					"is refused after it has already been paid for", f)
			}
		})
	}
}

// repoRoot walks up from this package to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the test's working directory")
		}
		dir = parent
	}
}
