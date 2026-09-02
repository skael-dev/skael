// Package sandboxtest holds the contract every sandbox.Driver must satisfy.
// It lives outside package sandbox so "testing" never reaches production code.
package sandboxtest

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

// RunConformance asserts the workspace mirrors in both directions: a file
// staged before the run is readable inside it, and a file the run writes is
// readable on the caller's disk afterwards. A driver that mirrors only inward
// records every session as a skill that produced nothing.
func RunConformance(t *testing.T, d sandbox.Driver, base sandbox.EnvSpec) {
	t.Helper()
	ctx := context.Background()

	img, err := d.Prepare(ctx, base)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "in.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("staging in.txt: %v", err)
	}

	var stdout, stderr bytes.Buffer
	res, err := d.Run(ctx, sandbox.RunSpec{
		Image:     img,
		Workspace: ws,
		Argv:      []string{"sh", "-c", "cat in.txt > out.txt"},
		Network:   sandbox.NetNone,
		Timeout:   2 * time.Minute,
		Stdout:    &stdout,
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr.String())
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr: %s", res.ExitCode, stderr.String())
	}

	got, err := os.ReadFile(filepath.Join(ws, "out.txt"))
	if err != nil {
		t.Fatalf("reading out.txt back from the workspace: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("out.txt = %q, want %q", got, "hello")
	}
}
