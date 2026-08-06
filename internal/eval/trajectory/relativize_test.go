package trajectory_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/trajectory"
)

// The agent CLI reports absolute container paths while contract.MatchPath
// compares workspace-relative ones and rejects an absolute candidate outright.
func TestRelativize(t *testing.T) {
	const root = "/workspace"

	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "a container path becomes workspace-relative",
			in:   []string{"/workspace/environment/docs/sdd.md"},
			want: []string{"environment/docs/sdd.md"},
		},
		{
			name: "the skill's own files relativise too",
			in:   []string{"/workspace/.claude/skills/demo/SKILL.md"},
			want: []string{".claude/skills/demo/SKILL.md"},
		},
		{
			// The relativiser must not invent a relationship that isn't
			// there: a genuine escape has to stay absolute so MatchPath keeps
			// reporting it as unevaluable rather than matching it by accident.
			name: "a path outside the workspace is left alone",
			in:   []string{"/etc/passwd"},
			want: []string{"/etc/passwd"},
		},
		{
			name: "an already-relative path is untouched",
			in:   []string{"out/report.md"},
			want: []string{"out/report.md"},
		},
		{
			// The workspace root itself carries no file, and "." matches no
			// pattern this package compiles.
			name: "the root itself is left alone",
			in:   []string{"/workspace"},
			want: []string{"/workspace"},
		},
		{
			// A sibling directory sharing the root's textual prefix is not
			// under the root; a naive strings.TrimPrefix would corrupt it.
			name: "a sibling sharing the prefix is not under the root",
			in:   []string{"/workspace-other/file.md"},
			want: []string{"/workspace-other/file.md"},
		},
		{name: "no paths", in: nil, want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events := []trajectory.Event{{Seq: 1, Type: trajectory.TypeFileWrite, Paths: tc.in}}
			got := trajectory.Relativize(events, root)
			if len(got) != 1 {
				t.Fatalf("got %d events, want 1", len(got))
			}
			if len(got[0].Paths) != len(tc.want) {
				t.Fatalf("Paths = %v, want %v", got[0].Paths, tc.want)
			}
			for i := range tc.want {
				if got[0].Paths[i] != tc.want[i] {
					t.Errorf("Paths[%d] = %q, want %q", i, got[0].Paths[i], tc.want[i])
				}
			}
		})
	}
}

// An empty root means "no workspace known", which must be a no-op rather than
// a rewrite against "".
func TestRelativize_EmptyRootIsANoOp(t *testing.T) {
	in := []trajectory.Event{{Seq: 1, Paths: []string{"/workspace/a.md"}}}
	got := trajectory.Relativize(in, "")
	if got[0].Paths[0] != "/workspace/a.md" {
		t.Errorf("Paths[0] = %q, want it untouched", got[0].Paths[0])
	}
}

// Relativize must not mutate its input: events are persisted to events.jsonl
// and replayed on resume, and a caller that relativises for scoring must not
// silently change what another caller is about to write.
func TestRelativize_DoesNotMutateItsInput(t *testing.T) {
	in := []trajectory.Event{{Seq: 1, Paths: []string{"/workspace/a.md"}}}
	_ = trajectory.Relativize(in, "/workspace")
	if in[0].Paths[0] != "/workspace/a.md" {
		t.Errorf("input was mutated: Paths[0] = %q", in[0].Paths[0])
	}
}
