package kubernetes

import (
	"context"
	"strings"
	"testing"

	fake "k8s.io/client-go/kubernetes/fake"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/spec"
)

func newTestDriver(t *testing.T, o Options) *Driver {
	t.Helper()
	d, err := New(o, fake.NewSimpleClientset(), &tarExecer{remote: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

func TestHardwareIsolated_IsFalseUntilTheOperatorAssertsIt(t *testing.T) {
	if newTestDriver(t, validOptions()).HardwareIsolated() {
		t.Error("HardwareIsolated must default to false, so sandbox.CheckPolicy refuses untrusted work")
	}
	o := validOptions()
	o.HardwareIsolated, o.RuntimeClass = true, "kata"
	if !newTestDriver(t, o).HardwareIsolated() {
		t.Error("HardwareIsolated must be true once asserted with a runtime class")
	}
}

// Building an image needs a daemon this driver does not have. Running the
// skill anyway on a base that lacks its dependencies would record the
// resulting failures as the skill's fault.
func TestPrepare_RefusesADeclaredDependencyByName(t *testing.T) {
	d := newTestDriver(t, validOptions())
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
	o := validOptions()
	d := newTestDriver(t, o)
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
