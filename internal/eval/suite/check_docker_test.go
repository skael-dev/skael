//go:build docker

package suite_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/docker"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// dockerDriver builds the base image once per test binary. WHETSTONE_BASE_TAG
// lets CI point at the slim image; locally the default full image is used.
func dockerDriver(t *testing.T) *docker.Driver {
	t.Helper()
	baseTag := os.Getenv("WHETSTONE_BASE_TAG")
	slim := baseTag == imagespec.SlimBaseTag
	d, err := docker.New(docker.Options{BaseTag: baseTag, Logger: t.Logf})
	if err != nil {
		t.Fatalf("docker.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if err := d.EnsureBase(ctx, slim); err != nil {
		t.Fatalf("EnsureBase: %v", err)
	}
	return d
}

// realSuite is a three-task suite of genuine shell, exercising the three
// verdicts an oracle gate must tell apart:
//
//   - "sound": the oracle writes the file its verifier checks for. Neither
//     side is broken, so the task must not be void.
//   - "broken-oracle": the oracle exits non-zero. The task is unsolvable as
//     written, regardless of what its verifier does.
//   - "toothless-verifier": the oracle solves the task, but the verifier is
//     `true` — it accepts any workspace, solved or not. It hands out a free
//     pass and must be void.
func realSuite() *suite.Suite {
	return &suite.Suite{Tasks: []suite.TaskPkg{
		{
			ID:       "sound",
			Kind:     "happy",
			PromptMD: "write ok to answer.txt",
			Oracle:   "#!/bin/bash\nset -e\necho ok > answer.txt\n",
			Verifier: "#!/bin/bash\nset -e\ntest -f answer.txt\ngrep -q ok answer.txt\n",
		},
		{
			ID:       "broken-oracle",
			Kind:     "happy",
			PromptMD: "an oracle that cannot solve its own task",
			Oracle:   "#!/bin/bash\nexit 1\n",
			Verifier: "#!/bin/bash\nset -e\ntest -f answer.txt\n",
		},
		{
			ID:       "toothless-verifier",
			Kind:     "happy",
			PromptMD: "a verifier that asserts nothing",
			Oracle:   "#!/bin/bash\nset -e\necho ok > answer.txt\n",
			Verifier: "#!/bin/bash\ntrue\n",
		},
	}}
}

func TestCheck_GatesARealSuiteThroughTheDockerDriver(t *testing.T) {
	d := dockerDriver(t)
	baseTag := os.Getenv("WHETSTONE_BASE_TAG")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	image, err := d.Prepare(ctx, sandbox.EnvSpec{Skill: "check-docker-test", BaseTag: baseTag})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	dir := t.TempDir()
	s := realSuite()
	if err := s.Write(dir); err != nil {
		t.Fatal(err)
	}

	results, err := suite.Check(context.Background(), s, suite.CheckOptions{
		Driver: d, Image: image, SuiteDir: dir,
		Timeout: 2 * time.Minute, Concurrency: 3, Logger: t.Logf,
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}

	byID := map[string]suite.CheckResult{}
	for _, r := range results {
		byID[r.TaskID] = r
	}

	if r := byID["sound"]; r.Void {
		t.Errorf("sound: void despite a working oracle and a discriminating verifier: %s", r.Reason)
	}

	if r := byID["broken-oracle"]; !r.Void {
		t.Error("broken-oracle: not void despite the oracle exiting non-zero")
	} else if !strings.Contains(r.Reason, "oracle") {
		t.Errorf("broken-oracle: reason = %q, want it to name the oracle", r.Reason)
	}

	if r := byID["toothless-verifier"]; !r.Void {
		t.Error("toothless-verifier: not void despite a verifier that accepts a bare workspace")
	} else if !strings.Contains(r.Reason, "without") {
		t.Errorf("toothless-verifier: reason = %q, want it to say the verifier passes without the oracle", r.Reason)
	}

	voidSet := suite.VoidSet(results)
	if len(voidSet) != 2 || !voidSet["broken-oracle"] || !voidSet["toothless-verifier"] {
		t.Errorf("VoidSet = %v, want exactly broken-oracle and toothless-verifier", voidSet)
	}
}
