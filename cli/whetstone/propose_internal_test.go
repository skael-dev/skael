package whetstone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/cli/client"
)

func TestLossesFor_KeepsOneCandidate(t *testing.T) {
	c := &client.Contest{Losses: []client.ContestLoss{
		{Label: "a@1", TaskID: "1", Missed: []string{"tags the release"}},
		{Label: "b@2", TaskID: "1", Missed: []string{"writes a rollback note"}},
		{Label: "a@1", TaskID: "3", Missed: []string{"checks the health endpoint"}},
	}}
	got := lossesFor(c, "a@1")
	if len(got) != 2 {
		t.Fatalf("%d losses, want 2", len(got))
	}
	for _, l := range got {
		if len(l.Missed) == 1 && l.Missed[0] == "writes a rollback note" {
			t.Error("another candidate's loss was included")
		}
	}
}

// A candidate that won still has losses to work from, and the command must
// reach them.
func TestLossesFor_ReachesAWinnersLosses(t *testing.T) {
	c := &client.Contest{
		Outcome: "winner", Winner: "a@1",
		Losses: []client.ContestLoss{{Label: "a@1", TaskID: "7", Missed: []string{"tags the release"}}},
	}
	if len(lossesFor(c, "a@1")) != 1 {
		t.Error("the winner's loss was not reachable")
	}
}

// Prose only. A bundle's scripts are not a proposal's business.
func TestReadProse_SkipsEverythingThatIsNotProse(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("SKILL.md", "# skill\n")
	write("references/release.md", "push\n")
	write("notes.txt", "note\n")
	write("scripts/deploy.sh", "#!/bin/sh\n")
	write("logo.png", "\x89PNG")

	files, err := readProse(dir)
	if err != nil {
		t.Fatalf("readProse: %v", err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	want := "SKILL.md notes.txt references/release.md"
	if strings.Join(paths, " ") != want {
		t.Errorf("paths = %v, want %s", paths, want)
	}
}

func TestDiff_ShowsOnlyWhatChanged(t *testing.T) {
	out := diff("one\ntwo\nthree\n", "one\ntwo and a half\nthree\n")
	if !strings.Contains(out, "two and a half") {
		t.Error("the added line is missing")
	}
	if !strings.Contains(out, "- two") {
		t.Error("the removed line is missing")
	}
	if strings.Contains(out, "+ one") || strings.Contains(out, "- three") {
		t.Errorf("an unchanged line was printed:\n%s", out)
	}
}
