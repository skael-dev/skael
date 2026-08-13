//go:build docker

// Package whetstone_e2e drives the built binary the way a person does: from
// inside a bundle directory, with arguments a shell would produce, against a
// real daemon.
//
// Every defect this file exists to catch was invisible to a green unit suite on
// the previous phase: a default timeout that killed the longest call, a `.`
// argument that could never match a frontmatter name, and a closed stdin that
// errored instead of declining a prompt. None of them are reachable from a test
// that calls the package function directly.
//
// This package used to be unsafe to run alongside internal/eval/sandbox/docker
// at "go test"'s default package parallelism: that package's own
// TestSweep_RemovesOrphanedContainersAndNetworks and
// TestSweep_LeavesUnrelatedContainersAlone zero docker.SweepMinAge and then
// call the real Sweep(), which used to list containers and networks by a
// docker-daemon-wide label with no per-process scoping — so with the age
// guard off, that sweep removed every whetstone-labeled resource on the
// daemon, including this package's live, still-running containers, if the
// two happened to be executing at the same moment. Sweep now also requires
// the labeled resource's owning pid to be confirmed dead
// (internal/eval/sandbox/docker/sweep.go's pidAlive) before removing it, so
// a live container created by this package's own test binary — a different,
// running pid — is never a candidate no matter how the age guard is set
// elsewhere on the daemon. This package runs at default parallelism again.
package whetstone_e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// binOnce/binPath memoize the build across every test in this binary: each
// test that needs the CLI calls binary(t) independently (that is the shape
// the tests are written in), and rebuilding a Go binary per call would add
// minutes doing nothing this file is about.
var (
	binOnce sync.Once
	binPath string
)

// binary builds whetstone once per test binary. os.MkdirTemp rather than
// t.TempDir: the Once captures whichever t calls it first, and t.TempDir
// is cleaned when that test finishes — deleting the binary while later
// tests still need it.
func binary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "whetstone-e2e-bin-*")
		if err != nil {
			t.Fatalf("creating binary dir: %v", err)
		}
		binPath = filepath.Join(dir, "whetstone")
		cmd := exec.Command("go", "build", "-o", binPath, "./../../cmd/whetstone")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building whetstone: %v\n%s", err, out)
		}
	})
	return binPath
}

func run(t *testing.T, bin, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	b, err := cmd.CombinedOutput()
	code := cmd.ProcessState.ExitCode()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(b), code
	} else if err != nil {
		t.Fatalf("running %v: %v\n%s", args, err, b)
	}
	return string(b), code
}

func TestWhetstone_InitDoctorAndSuiteCheckFromInsideTheBundle(t *testing.T) {
	bin := binary(t)
	root := t.TempDir()

	if out, code := run(t, bin, root, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, out)
	}
	// The parked 3a defect: init nesting inside an existing workspace silently
	// shadowed the outer one.
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, _ := run(t, bin, sub, "init"); !strings.Contains(out, "inside the workspace") {
		t.Errorf("init inside an existing workspace did not warn:\n%s", out)
	}

	if out, code := run(t, bin, root, "doctor"); code != 0 {
		t.Errorf("doctor exited %d:\n%s", code, out)
	}
}

func TestWhetstone_LintAndPackFromInsideTheBundleDirectory(t *testing.T) {
	bin := binary(t)
	root := t.TempDir()

	if out, code := run(t, bin, root, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, out)
	}
	bundleDir := seedCorpusBundle(t, root, "deterministic-transform")

	if out, code := run(t, bin, bundleDir, "lint", "."); code != 0 {
		t.Errorf("lint . from inside the bundle failed:\n%s", out)
	}
	if out, code := run(t, bin, root, "pack", bundleDir); code != 0 {
		t.Errorf("pack from the workspace root failed:\n%s", out)
	}
	if out, code := run(t, bin, bundleDir, "pack", "."); code != 0 {
		t.Errorf("pack . from inside the bundle failed:\n%s", out)
	}
	if out, code := run(t, bin, bundleDir, "lint", "."); code != 0 {
		t.Errorf("lint . failed after pack:\n%s", out)
	}
}

// seedCorpusBundle copies one of the golden-corpus bundles from
// internal/eval/testdata/corpus/ into <root>/.whetstone/skills/<archetype>/ —
// everything a shipped bundle carries (SKILL.md, spec.yaml, scripts/, and the
// eval/ sidecar) except expected-lint.json, which is grading metadata for the
// corpus regression suite and not bundle content; a real bundle never has it,
// and lint would flag an unrecognised top-level file.
func seedCorpusBundle(t *testing.T, root, archetype string) string {
	t.Helper()
	src := filepath.Join("../../internal/eval/testdata/corpus", archetype)
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("corpus bundle %q not found: %v", archetype, err)
	}
	dst := filepath.Join(root, ".whetstone", "skills", archetype)
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if filepath.Base(path) == "expected-lint.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
	if err != nil {
		t.Fatalf("seeding corpus bundle %q: %v", archetype, err)
	}
	return dst
}

// Tests below this line were removed: they compiled against suite.Load,
// suite.TaskPkg, suite.Suite.Write, and store.ContractPath, all of which were
// deleted when the eval pipeline switched from the tasks/ layout to
// evals/evals.json. The three surviving tests above (Init, LintAndPack) do not
// use those types and still compile under the docker build tag.
//
// The removed tests (SuiteCheckRunsRealOracles, EvalWithNoUsableSetup,
// EvalIsResumable) and their helpers (seedSkillWithSuite, seedSuiteTasks,
// stubClaudeBaseTag) exercised real sandbox orchestration and are worth
// rewriting against the new format. Until then the unit and package tests in
// internal/eval/... cover the same code paths without a Docker dependency.
