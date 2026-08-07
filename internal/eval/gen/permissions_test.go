package gen_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/internal/eval/gen"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// TestGenerate_ScriptsAreExecutableOtherResourcesAreNot asserts the binding
// mode contract: a file written under scripts/ must be executable, because a
// later sandbox stage runs generated scripts directly — a non-executable
// script fails there, far from the cause, in a stage where each failure
// costs a model session to observe. A file outside scripts/ must not pick up
// those bits.
//
// Permission bits are checked with Perm()&0o111 rather than an exact mode
// comparison, so the test isn't brittle against a umask that clears bits
// os.WriteFile never asked for in the first place.
func TestGenerate_ScriptsAreExecutableOtherResourcesAreNot(t *testing.T) {
	s := genSpec()
	s.Resources = spec.ResourcePlan{
		Scripts:    []spec.ResourceItem{{Path: "scripts/extract.py"}},
		References: []spec.ResourceItem{{Path: "references/format.md"}},
	}
	responses := []string{
		`{"sections":["Overview","Steps","Failure handling"]}`,
		`{"body":"# PDF Extract\n\n1. Run ` + "`scripts/extract.py <input.pdf>`" + `. Postcondition: out/tables.csv exists.\n\nIf a checkpoint cannot be satisfied after one retry, stop and report state.\n"}`,
		`{"content":"#!/usr/bin/env python3\n"}`,
		`{"content":"# Format\n"}`,
		`{"description":"Extracts tables from PDF files into CSV. Use when the user mentions a PDF."}`,
	}

	out := t.TempDir()
	b, err := gen.Generate(context.Background(), fake.New(responses...), s, out)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	scriptInfo, err := os.Stat(filepath.Join(b.Dir, "scripts", "extract.py"))
	if err != nil {
		t.Fatalf("scripts/extract.py not written: %v", err)
	}
	if scriptInfo.Mode().Perm()&0o111 == 0 {
		t.Errorf("scripts/extract.py has mode %v, want at least one executable bit set", scriptInfo.Mode().Perm())
	}

	refInfo, err := os.Stat(filepath.Join(b.Dir, "references", "format.md"))
	if err != nil {
		t.Fatalf("references/format.md not written: %v", err)
	}
	if refInfo.Mode().Perm()&0o111 != 0 {
		t.Errorf("references/format.md has mode %v, want no executable bits", refInfo.Mode().Perm())
	}
}
