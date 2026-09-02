//go:build unix

package northflank

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCLI puts a script named "northflank" on PATH that records its argv and
// exits with the given code.
func fakeCLI(t *testing.T, exitCode int) (dir, logPath string) {
	t.Helper()
	dir = t.TempDir()
	logPath = filepath.Join(dir, "argv.log")
	script := "#!/bin/sh\necho \"$@\" >> " + logPath + "\nexit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "northflank"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir, logPath
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	return string(rune('0' + i))
}

func TestUploadWorkspace_InvokesTheCLIWithTheLocalAndRemotePaths(t *testing.T) {
	_, logPath := fakeCLI(t, 0)
	d := &Driver{o: validOptions().withDefaults()}
	local := t.TempDir()

	if err := d.uploadWorkspace(context.Background(), "svc-1", local, "/workspace"); err != nil {
		t.Fatalf("uploadWorkspace: %v", err)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"svc-1", local, "/workspace", validOptions().Project} {
		if !strings.Contains(string(logged), want) {
			t.Errorf("CLI argv does not carry %q: %s", want, logged)
		}
	}
}

// A failed copy back is indistinguishable from a skill that produced nothing,
// and would be graded as one.
func TestDownloadWorkspace_ReturnsAnErrorWhenTheCLIFails(t *testing.T) {
	fakeCLI(t, 1)
	d := &Driver{o: validOptions().withDefaults()}
	err := d.downloadWorkspace(context.Background(), "svc-1", "/workspace", t.TempDir())
	if err == nil {
		t.Fatal("downloadWorkspace: want an error when the CLI exits non-zero")
	}
}

// Only cliLogin may carry the token, and it runs once at construction. A
// transfer that carried it would put the token in a process listing on every
// workspace copy.
func TestUploadWorkspace_NeverPutsTheTokenInArgv(t *testing.T) {
	_, logPath := fakeCLI(t, 0)
	d := &Driver{o: validOptions().withDefaults()}
	if err := d.uploadWorkspace(context.Background(), "svc-1", t.TempDir(), "/workspace"); err != nil {
		t.Fatalf("uploadWorkspace: %v", err)
	}
	logged, _ := os.ReadFile(logPath)
	if strings.Contains(string(logged), validOptions().Token) {
		t.Errorf("the API token appears in the CLI argv: %s", logged)
	}
}

func TestUploadWorkspace_NamesTheMissingBinaryWhenTheCLIIsAbsent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	d := &Driver{o: validOptions().withDefaults()}
	err := d.uploadWorkspace(context.Background(), "svc-1", t.TempDir(), "/workspace")
	if err == nil || !strings.Contains(err.Error(), "northflank") {
		t.Fatalf("err = %v, want an error naming the missing CLI", err)
	}
}
