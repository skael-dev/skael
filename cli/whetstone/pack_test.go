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

// TestRunPack_DoesNotArchiveItsOwnOutput covers the most natural invocation:
// packing the bundle you are standing in, with the default output name. The
// archive is created inside the directory about to be walked, so a walk that
// starts after the file exists packs a truncated copy of the archive into
// itself.
func TestRunPack_DoesNotArchiveItsOwnOutput(t *testing.T) {
	dir := writeSkill(t, "pdf-extract", cleanSkillMD)
	t.Chdir(dir)

	out := "pdf-extract.tar.gz"
	if err := whetstone.RunPack(".", out); err != nil {
		t.Fatalf("RunPack: %v", err)
	}

	for _, name := range namesIn(t, out) {
		if filepath.Base(name) == out {
			t.Errorf("packed archive contains its own output file: %s", name)
		}
		if strings.HasSuffix(name, ".tar.gz") {
			t.Errorf("packed archive contains an archive: %s", name)
		}
	}

	// The archive is built in a temp file; nothing may be left behind, and the
	// finished file must be readable by someone other than its author.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".whetstone-pack-") {
			t.Errorf("pack left a temp file behind: %s", e.Name())
		}
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("archive mode = %#o, want 0644", info.Mode().Perm())
	}
}

// TestRunPack_LintsTheBundleUnderItsRealName covers packing and linting the
// directory you are standing in. lint derives the skill's expected name from
// the last element of the directory it is handed, so a literal "." is compared
// against the frontmatter name and every clean bundle fails.
func TestRunPack_LintsTheBundleUnderItsRealName(t *testing.T) {
	dir := writeSkill(t, "pdf-extract", cleanSkillMD)
	t.Chdir(dir)

	code, err := whetstone.RunLint(".", false)
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	if code != 0 {
		t.Errorf("linting the current directory exit code = %d, want 0", code)
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
