package kubernetes

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// tarExecer answers a stage-in by unpacking what it is given, and a
// collect-out by tarring a directory it holds. It stands in for a pod.
type tarExecer struct {
	remote  string
	failOut bool
}

func (e *tarExecer) Exec(_ context.Context, r execRequest) (int, error) {
	switch {
	case r.Stdin != nil:
		return 0, untarInto(e.remote, r.Stdin)
	case e.failOut:
		return 1, errors.New("tar: exited 1")
	default:
		return 0, tarDir(e.remote, r.Stdout)
	}
}

func TestStageInAndCollectOut_MirrorTheWorkspaceBothWays(t *testing.T) {
	local, remote := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(local, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "sub", "in.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := &Driver{o: validOptions().withDefaults(), ex: &tarExecer{remote: remote}}
	ctx := context.Background()

	if err := d.stageIn(ctx, "pod", "/workspace", local); err != nil {
		t.Fatalf("stageIn: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(remote, "sub", "in.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("staged file = %q, %v; want hello", got, err)
	}

	if err := os.WriteFile(filepath.Join(remote, "out.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.collectOut(ctx, "pod", "/workspace", local); err != nil {
		t.Fatalf("collectOut: %v", err)
	}
	got, err = os.ReadFile(filepath.Join(local, "out.txt"))
	if err != nil || string(got) != "world" {
		t.Fatalf("collected file = %q, %v; want world", got, err)
	}
}

// A partial mirror is indistinguishable from a skill that produced nothing, so
// it must surface as an error and never as a short result.
func TestCollectOut_ReturnsAnErrorRatherThanASilentlyEmptyWorkspace(t *testing.T) {
	local := t.TempDir()
	d := &Driver{o: validOptions().withDefaults(), ex: &tarExecer{remote: t.TempDir(), failOut: true}}
	if err := d.collectOut(context.Background(), "pod", "/workspace", local); err == nil {
		t.Fatal("collectOut: want an error when the copy back fails")
	}
}

// Extraction writes to the caller's disk from a stream the sandbox produced.
func TestUntarInto_RefusesAPathThatEscapesTheDestination(t *testing.T) {
	var buf bytes.Buffer
	writeEvilTar(t, &buf)
	dest := t.TempDir()
	if err := untarInto(dest, &buf); err == nil {
		t.Fatal("untarInto: want a refusal for ../escape")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escape")); err == nil {
		t.Fatal("untarInto wrote outside the destination")
	}
}

func writeEvilTar(t *testing.T, w io.Writer) {
	t.Helper()
	tw := tar.NewWriter(w)
	if err := tw.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o644, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
}

// The total extraction cap must reject a stream that would otherwise be
// buffered whole in the worker's memory (see maxWorkspaceTotalSize). Shrink
// both caps for the test rather than writing gigabytes of fixture data.
func TestUntarInto_RejectsAStreamOverTheTotalSizeCap(t *testing.T) {
	origFile, origTotal := maxWorkspaceFileSize, maxWorkspaceTotalSize
	maxWorkspaceFileSize, maxWorkspaceTotalSize = 1200, 1900 // two 1000-byte files trip the total cap, neither trips the per-file cap
	t.Cleanup(func() { maxWorkspaceFileSize, maxWorkspaceTotalSize = origFile, origTotal })

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// Two files, each under the per-file cap, so neither trips it alone, but
	// their sum trips the total cap.
	var perFile int64 = 1000
	payload := bytes.Repeat([]byte("x"), int(perFile))
	for _, name := range []string{"a.bin", "b.bin"} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: perFile}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	err := untarInto(dest, &buf)
	if err == nil {
		t.Fatal("untarInto: want an error when the total extraction size exceeds the cap")
	}
}
