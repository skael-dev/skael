package store_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/store"
)

func TestGeneratedRef_RoundTrips(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	if err := st.RecordGeneratedRef("pdf-extract", "abc123"); err != nil {
		t.Fatalf("RecordGeneratedRef: %v", err)
	}
	got, err := st.GeneratedRef("pdf-extract")
	if err != nil {
		t.Fatalf("GeneratedRef: %v", err)
	}
	if got != "abc123" {
		t.Errorf("GeneratedRef = %q, want abc123", got)
	}
}

// TestGeneratedRef_MissingRowIsNotAnError covers a suite that predates this
// table. The caller treats an absent row as a possible case of manual
// authorship. This is the safe answer: it pushes as authored rather than
// as unreviewed.
func TestGeneratedRef_MissingRowIsNotAnError(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	got, err := st.GeneratedRef("never-generated")
	if err != nil {
		t.Fatalf("GeneratedRef: %v", err)
	}
	if got != "" {
		t.Errorf("GeneratedRef = %q, want the empty string", got)
	}
}

// TestGeneratedRef_ARegenerationReplacesTheRecord pins that the table holds
// the latest generation, not a history. A stale ref marks an edited suite
// unreviewed.
func TestGeneratedRef_ARegenerationReplacesTheRecord(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	if err := st.RecordGeneratedRef("pdf-extract", "first"); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordGeneratedRef("pdf-extract", "second"); err != nil {
		t.Fatal(err)
	}
	got, err := st.GeneratedRef("pdf-extract")
	if err != nil {
		t.Fatal(err)
	}
	if got != "second" {
		t.Errorf("GeneratedRef = %q, want second", got)
	}
}
