package score

import "testing"

// TestResolveWinner is the table test required by the brief: this mapping is
// where a swap silently becomes a bug if it is inlined instead of named and
// tested in isolation.
func TestResolveWinner(t *testing.T) {
	skillFirst := Sample{Label: "skill"}
	baselineFirst := Sample{Label: "baseline"}

	tests := []struct {
		name   string
		letter string
		aWas   Sample
		want   string
	}{
		{"A resolves to the sample presented first, skill-first ordering", "A", skillFirst, "skill"},
		{"B resolves to the other sample, skill-first ordering", "B", skillFirst, "baseline"},
		{"A resolves to the sample presented first, baseline-first ordering", "A", baselineFirst, "baseline"},
		{"B resolves to the other sample, baseline-first ordering", "B", baselineFirst, "skill"},
		{"lowercase a behaves like uppercase A", "a", skillFirst, "skill"},
		{"lowercase b behaves like uppercase B", "b", baselineFirst, "skill"},
		{"tie is passed through regardless of ordering", "tie", skillFirst, "tie"},
		{"an unrecognised answer falls back to tie", "maybe", skillFirst, "tie"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveWinner(tt.letter, tt.aWas); got != tt.want {
				t.Errorf("resolveWinner(%q, %+v) = %q, want %q", tt.letter, tt.aWas, got, tt.want)
			}
		})
	}
}
