package whetstone_test

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/cli/whetstone"
)

func TestRunPack_StripsTheEvalSidecar(t *testing.T) {
	dir := writeSkill(t, "pdf-extract", cleanSkillMD)
	// The sidecar that must not ship.
	mustWrite(t, filepath.Join(dir, "eval", "contract.yaml"), "version: 1\n")
	mustWrite(t, filepath.Join(dir, "eval", "suite", "tasks", "t0", "task.md"), "do it")
	mustWrite(t, filepath.Join(dir, "spec.yaml"), "name: pdf-extract\n")

	out := filepath.Join(t.TempDir(), "pdf-extract.tar.gz")
	if err := whetstone.RunPack(dir, out); err != nil {
		t.Fatalf("RunPack: %v", err)
	}

	for _, name := range namesIn(t, out) {
		if strings.HasPrefix(name, "eval/") || strings.Contains(name, "/eval/") {
			t.Errorf("packed archive contains the eval sidecar: %s", name)
		}
		if strings.HasSuffix(name, "spec.yaml") {
			t.Errorf("packed archive contains the spec: %s", name)
		}
		if filepath.IsAbs(name) || strings.Contains(name, "..") {
			t.Errorf("packed archive contains an unsafe path: %s", name)
		}
	}

	var hasSkillMD bool
	for _, name := range namesIn(t, out) {
		if strings.HasSuffix(name, "SKILL.md") {
			hasSkillMD = true
		}
	}
	if !hasSkillMD {
		t.Error("packed archive has no SKILL.md")
	}
}

func TestRunPack_RefusesABundleThatFailsLint(t *testing.T) {
	// Packing an invalid bundle produces an archive that fails at install time,
	// far from the cause.
	dir := writeSkill(t, "pdf-extract", "---\nname: wrong-name\n---\n\n# x\n")
	out := filepath.Join(t.TempDir(), "x.tar.gz")
	if err := whetstone.RunPack(dir, out); err == nil {
		t.Error("RunPack accepted a bundle that fails lint")
	}
}

// TestRunPack_KeepsEverythingElse guards against the sidecar filter being
// written too broadly: a bundle's own scripts and references must survive.
func TestRunPack_KeepsEverythingElse(t *testing.T) {
	dir := writeSkill(t, "pdf-extract", cleanSkillMD)
	mustWrite(t, filepath.Join(dir, "scripts", "extract.py"), "print('x')\n")
	mustWrite(t, filepath.Join(dir, "references", "format.md"), "# format\n")
	mustWrite(t, filepath.Join(dir, "eval", "contract.yaml"), "version: 1\n")

	out := filepath.Join(t.TempDir(), "pdf-extract.tar.gz")
	if err := whetstone.RunPack(dir, out); err != nil {
		t.Fatalf("RunPack: %v", err)
	}

	got := map[string]bool{}
	for _, name := range namesIn(t, out) {
		got[name] = true
	}
	for _, want := range []string{"SKILL.md", "scripts/extract.py", "references/format.md"} {
		if !got[want] {
			t.Errorf("packed archive is missing %s (has %v)", want, namesIn(t, out))
		}
	}
}

func namesIn(t *testing.T, archive string) []string {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	var out []string
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err != nil {
			return out
		}
		out = append(out, h.Name)
	}
}
