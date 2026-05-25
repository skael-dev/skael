package skillimport

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover_FindsMultipleSkills(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills", "code-review", "SKILL.md"),
		"---\nname: code-review\ndescription: Reviews code\n---\nBody")
	writeFile(t, filepath.Join(dir, "skills", "code-review", "references", "guide.md"),
		"# Guide")
	writeFile(t, filepath.Join(dir, "skills", "docx", "SKILL.md"),
		"---\nname: docx\ndescription: Word docs\n---\nBody")
	writeFile(t, filepath.Join(dir, "README.md"), "# Repo")

	results, err := Discover(dir, "")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d skills, want 2", len(results))
	}

	names := map[string]bool{}
	for _, r := range results {
		names[r.Name] = true
	}
	if !names["code-review"] || !names["docx"] {
		t.Errorf("unexpected names: %v", results)
	}

	for _, r := range results {
		if r.Name == "code-review" {
			if len(r.Files) != 2 {
				t.Errorf("code-review: got %d files, want 2", len(r.Files))
			}
			if r.Description != "Reviews code" {
				t.Errorf("code-review: description = %q", r.Description)
			}
		}
	}
}

func TestDiscover_WithSpecificPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills", "foo", "SKILL.md"),
		"---\nname: foo\ndescription: Foo skill\n---\nBody")
	writeFile(t, filepath.Join(dir, "skills", "bar", "SKILL.md"),
		"---\nname: bar\ndescription: Bar skill\n---\nBody")

	results, err := Discover(dir, "skills/foo")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d skills, want 1", len(results))
	}
	if results[0].Name != "foo" {
		t.Errorf("name = %q, want %q", results[0].Name, "foo")
	}
}

func TestDiscover_NoSkills(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "README.md"), "# Hello")

	results, err := Discover(dir, "")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("got %d skills, want 0", len(results))
	}
}
