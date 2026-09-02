//go:build unix

package northflank

import (
	"context"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/spec"
)

func TestHardwareIsolated_IsFalseUntilTheOperatorAssertsIt(t *testing.T) {
	fakeCLI(t, 0)
	d, err := New(validOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d.HardwareIsolated() {
		t.Error("HardwareIsolated must default to false, so sandbox.CheckPolicy refuses untrusted work")
	}

	o := validOptions()
	o.HardwareIsolated = true
	d2, err := New(o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !d2.HardwareIsolated() {
		t.Error("HardwareIsolated must be true once the operator asserts it")
	}
}

// There is no daemon to build with, and running the skill on a base that lacks
// its dependencies would record the failures as the skill's own fault.
func TestPrepare_RefusesADeclaredDependencyByName(t *testing.T) {
	fakeCLI(t, 0)
	d, _ := New(validOptions())
	_, err := d.Prepare(context.Background(), sandbox.EnvSpec{
		Skill: "pdf-extract",
		Deps:  spec.DepsDecl{Pip: []string{"pdfplumber"}},
	})
	if err == nil {
		t.Fatal("Prepare: want a refusal")
	}
	if !strings.Contains(err.Error(), "pdfplumber") {
		t.Errorf("error %q must name the dependency it cannot satisfy", err)
	}
}

func TestPrepare_ReturnsTheConfiguredImageAndStillRecordsTheDigest(t *testing.T) {
	fakeCLI(t, 0)
	o := validOptions()
	o.Image = "ghcr.io/skael-dev/whetstone-base:1"
	d, _ := New(o)
	ref, err := d.Prepare(context.Background(), sandbox.EnvSpec{Skill: "plain"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if ref.Tag != o.Image {
		t.Errorf("Tag = %q, want the configured image %q", ref.Tag, o.Image)
	}
	// The digest is what attributes a score to an environment.
	if ref.DepsDigest == "" {
		t.Error("DepsDigest is empty; a score could not be attributed to an environment")
	}
}

func TestNew_RefusesAConfigurationItCannotUse(t *testing.T) {
	o := validOptions()
	o.Token = ""
	if _, err := New(o); err == nil {
		t.Fatal("New: want an error for a missing token")
	}
}
