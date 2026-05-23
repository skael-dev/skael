package skill

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestPack_RequiresSkillMD verifies that Pack returns an error when the
// directory does not contain a SKILL.md file.
func TestPack_RequiresSkillMD(t *testing.T) {
	dir := t.TempDir()

	_, _, _, err := Pack(dir)
	if err == nil {
		t.Fatal("expected error when SKILL.md is missing, got nil")
	}
}

// TestPack_RoundTrip verifies a full pack → unpack cycle:
//   - Creates a temp dir with SKILL.md and scripts/run.sh
//   - Calls Pack and checks archive bytes, checksum, and manifest
//   - Calls Unpack into a second temp dir
//   - Verifies the unpacked files match the originals
func TestPack_RoundTrip(t *testing.T) {
	srcDir := t.TempDir()

	// Create SKILL.md
	skillMDContent := "---\nname: test-skill\n---\nThis is a test skill."
	if err := os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte(skillMDContent), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	// Create scripts/run.sh
	if err := os.MkdirAll(filepath.Join(srcDir, "scripts"), 0755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	runSHContent := "#!/bin/sh\necho hello"
	if err := os.WriteFile(filepath.Join(srcDir, "scripts", "run.sh"), []byte(runSHContent), 0755); err != nil {
		t.Fatalf("write run.sh: %v", err)
	}

	// Pack
	archiveBytes, checksum, manifest, err := Pack(srcDir)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	// Validate archive bytes are non-empty
	if len(archiveBytes) == 0 {
		t.Fatal("expected non-empty archive bytes")
	}

	// Validate checksum is a non-empty hex string (sha256 = 64 hex chars)
	if len(checksum) != 64 {
		t.Fatalf("expected 64-char sha256 hex checksum, got %d chars: %q", len(checksum), checksum)
	}

	// Validate manifest contains both files
	if len(manifest) != 2 {
		t.Fatalf("expected 2 manifest entries, got %d", len(manifest))
	}
	paths := make(map[string]int64)
	for _, fe := range manifest {
		paths[fe.Path] = fe.Size
	}
	if _, ok := paths["SKILL.md"]; !ok {
		t.Error("manifest missing SKILL.md")
	}
	if _, ok := paths["scripts/run.sh"]; !ok {
		t.Error("manifest missing scripts/run.sh")
	}

	// Unpack into a fresh directory
	destDir := t.TempDir()
	if err := Unpack(bytes.NewReader(archiveBytes), destDir); err != nil {
		t.Fatalf("Unpack: %v", err)
	}

	// Verify SKILL.md contents
	gotSkillMD, err := os.ReadFile(filepath.Join(destDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("read unpacked SKILL.md: %v", err)
	}
	if string(gotSkillMD) != skillMDContent {
		t.Errorf("SKILL.md mismatch:\ngot:  %q\nwant: %q", gotSkillMD, skillMDContent)
	}

	// Verify scripts/run.sh contents
	gotRunSH, err := os.ReadFile(filepath.Join(destDir, "scripts", "run.sh"))
	if err != nil {
		t.Fatalf("read unpacked run.sh: %v", err)
	}
	if string(gotRunSH) != runSHContent {
		t.Errorf("run.sh mismatch:\ngot:  %q\nwant: %q", gotRunSH, runSHContent)
	}
}

// TestParseFrontmatter verifies that YAML frontmatter is correctly parsed and
// the body (content after the closing "---") is returned separately.
func TestParseFrontmatter(t *testing.T) {
	input := "---\nname: code-review\ndescription: Review checklist\n---\n# Code Review\nDo the review."

	fm, body, err := ParseFrontmatter(input)
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}

	if fm == nil {
		t.Fatal("expected non-nil frontmatter map")
	}
	if got := fm["name"]; got != "code-review" {
		t.Errorf("fm[\"name\"]: got %q, want %q", got, "code-review")
	}
	if got := fm["description"]; got != "Review checklist" {
		t.Errorf("fm[\"description\"]: got %q, want %q", got, "Review checklist")
	}

	wantBody := "# Code Review\nDo the review."
	if body != wantBody {
		t.Errorf("body:\ngot:  %q\nwant: %q", body, wantBody)
	}
}

// TestParseFrontmatter_NoFrontmatter verifies that content without frontmatter
// returns nil map and the content unchanged.
func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	input := "# Just a heading\nNo frontmatter here."

	fm, body, err := ParseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm != nil {
		t.Errorf("expected nil frontmatter map, got %v", fm)
	}
	if body != input {
		t.Errorf("body:\ngot:  %q\nwant: %q", body, input)
	}
}
