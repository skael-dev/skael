package whetstone

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
)

// TestRunSpecEdit_ApprovesTheVersionItStores pins the second half of the
// no-gate rule. An edited spec that stayed unapproved made `whetstone eval`
// refuse the result until a third command ran, which is a gate on editing.
func TestRunSpecEdit_ApprovesTheVersionItStores(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	sp := &spec.SkillSpec{
		Name:        "pdf-extract",
		Purpose:     "Extract tables from PDFs.",
		Description: "Extracts tables from PDF files into CSV.",
		Triggers:    []spec.TriggerPhrase{{Text: "extract tables from this pdf"}},
		Steps:       []spec.Step{{ID: "s1", Action: "Run the script", Postcondition: "out/tables.csv exists"}},
		TargetTier:  spec.TierMid,
	}
	first, err := st.SaveSpec(sp)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ApproveSpec(sp.Name, first); err != nil {
		t.Fatal(err)
	}

	// A fake editor that changes one field, so sameSpec sees a real edit.
	editor := filepath.Join(t.TempDir(), "editor.sh")
	script := "#!/bin/sh\nsed -i.bak 's/Extract tables from PDFs./Extract tables and charts from PDFs./' \"$1\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", editor)
	t.Chdir(dir)

	if err := RunSpecEdit("pdf-extract"); err != nil {
		t.Fatalf("RunSpecEdit: %v", err)
	}

	_, version, err := st.LoadSpec("pdf-extract")
	if err != nil {
		t.Fatal(err)
	}
	if version == first {
		t.Fatalf("no new version stored; the edit did not take")
	}
	if !isApproved(st, "pdf-extract", version) {
		t.Errorf("spec version %d is not approved after an edit", version)
	}
}
