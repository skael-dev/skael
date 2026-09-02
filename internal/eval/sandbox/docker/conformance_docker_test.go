//go:build docker

package docker_test

import (
	"context"
	"testing"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/docker"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
	"github.com/skael-dev/skael/internal/eval/sandbox/sandboxtest"
)

func TestDockerDriver_SatisfiesTheWorkspaceContract(t *testing.T) {
	d, err := docker.New(docker.Options{BaseTag: imagespec.SlimBaseTag})
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	if err := d.EnsureBase(context.Background(), true); err != nil {
		t.Fatalf("EnsureBase: %v", err)
	}
	t.Cleanup(func() { d.Sweep(context.Background()) })

	sandboxtest.RunConformance(t, d, sandbox.EnvSpec{Skill: "conformance", BaseTag: imagespec.SlimBaseTag})
}
