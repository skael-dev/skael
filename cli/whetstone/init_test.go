package whetstone_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	whetstone "github.com/skael-dev/skael/cli/whetstone"
)

func TestRunInit_RefusesToNestInsideAnExistingWorkspace(t *testing.T) {
	outer := t.TempDir()
	if _, err := whetstone.RunInit(outer); err != nil {
		t.Fatalf("RunInit(outer): %v", err)
	}

	inner := filepath.Join(outer, "sub")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := whetstone.RunInit(inner)
	if err == nil {
		t.Fatal("RunInit nested silently; every later command in sub/ would shadow the outer workspace")
	}
	if !strings.Contains(err.Error(), outer) {
		t.Errorf("err = %v, want it to name the workspace it found at %s", err, outer)
	}
}
