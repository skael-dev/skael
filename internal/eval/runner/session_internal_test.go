package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/internal/eval/store"
)

// TestOutcomeFromRecord_EmptyArtifactDirDoesNotReadTheCwd pins a regression:
// filepath.Join("", gradingFileName) resolves to the bare relative filename,
// which os.Open resolves against the process's cwd rather than failing. A
// resumed run with no recorded ArtifactDir must fall back to the store's own
// columns, not silently pick up an unrelated grading.json sitting in
// whatever directory the test (or whetstone eval) happens to run from.
func TestOutcomeFromRecord_EmptyArtifactDirDoesNotReadTheCwd(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	strayPath := filepath.Join(wd, gradingFileName)
	if _, err := os.Stat(strayPath); err == nil {
		t.Fatalf("refusing to run: %s already exists", strayPath)
	}
	if err := os.WriteFile(strayPath, []byte(`{"Meta":{"InputTokens":999}}`), 0o644); err != nil {
		t.Fatalf("staging a stray grading.json in cwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(strayPath) })

	rec := store.RunRecord{
		Outcome: store.RunOutcome{
			ArtifactDir:  "",
			InputTokens:  10,
			OutputTokens: 20,
			DurationMS:   1000,
			AgentVersion: "v1",
		},
	}

	out := outcomeFromRecord(rec)

	if out.Meta.InputTokens == 999 {
		t.Error("the stray grading.json in cwd was read: an empty ArtifactDir must not resolve to a relative path")
	}
	if !out.MetaPartial {
		t.Error("MetaPartial = false, want true: an empty ArtifactDir must fall back to the store columns")
	}
	if out.Meta.InputTokens != 10 || out.Meta.OutputTokens != 20 {
		t.Errorf("Meta = %+v, want the store columns carried through", out.Meta)
	}
}
