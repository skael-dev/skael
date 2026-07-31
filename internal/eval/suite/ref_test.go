package suite_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/internal/eval/suite"
)

func writeSuiteTree(t *testing.T, dir string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "tasks", "t1", "oracle"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"triggers.yaml":            "positive:\n  - do the thing\nnegative:\n  - unrelated\n",
		"tasks/t1/task.md":         body,
		"tasks/t1/oracle/solve.sh": "#!/bin/sh\ntrue\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRef_IsStableAndContentAddressed(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeSuiteTree(t, a, "do the thing")
	writeSuiteTree(t, b, "do the thing")

	ra, err := suite.Ref(a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := suite.Ref(b)
	if err != nil {
		t.Fatal(err)
	}
	// Two identical suites in different directories must hash the same, or a
	// score can never be known to have been measured against the same tasks.
	if ra != rb {
		t.Errorf("identical suites hashed differently: %s vs %s", ra, rb)
	}

	writeSuiteTree(t, b, "do the thing differently")
	rc, err := suite.Ref(b)
	if err != nil {
		t.Fatal(err)
	}
	// And a changed task must change the ref, or a trend silently mixes suites
	// — worse than having no trend at all.
	if rc == ra {
		t.Error("ref did not change when a task's prompt changed")
	}
}
