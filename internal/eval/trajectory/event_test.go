package trajectory_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/trajectory"
)

func TestEvent_JSONShape(t *testing.T) {
	exit := 0
	e := trajectory.Event{
		Seq:        3,
		TS:         time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		Type:       trajectory.TypeShell,
		Name:       "bash",
		ArgsDigest: trajectory.Digest("scripts/extract.py in.pdf"),
		Paths:      []string{"scripts/extract.py"},
		ExitCode:   &exit,
	}

	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	for _, key := range []string{"seq", "ts", "type", "name", "args_digest", "paths", "exit_code"} {
		if _, ok := got[key]; !ok {
			t.Errorf("marshalled event missing key %q: %s", key, b)
		}
	}
	// text_digest was empty and must be omitted, not rendered as "".
	if _, ok := got["text_digest"]; ok {
		t.Errorf("empty text_digest should be omitted: %s", b)
	}
	// exit_code 0 must survive. A plain int with omitempty would drop it, and a
	// dropped zero exit code turns every success into "no exit code recorded".
	if got["exit_code"] != float64(0) {
		t.Errorf("exit_code = %v, want 0", got["exit_code"])
	}
}

func TestOpaqueIsExcludedFromContractables(t *testing.T) {
	events := []trajectory.Event{
		{Seq: 1, Type: trajectory.TypeShell},
		{Seq: 2, Type: trajectory.TypeOpaque, Name: "some_future_tool"},
		{Seq: 3, Type: trajectory.TypeFileWrite, Paths: []string{"out/x.csv"}},
	}

	got := trajectory.Contractable(events)
	if len(got) != 2 {
		t.Fatalf("Contractable returned %d events, want 2: %+v", len(got), got)
	}
	for _, e := range got {
		if e.Type == trajectory.TypeOpaque {
			t.Errorf("opaque event %d leaked into contractables — it would be scored as a violation", e.Seq)
		}
	}
	if got[0].Seq != 1 || got[1].Seq != 3 {
		t.Errorf("Contractable reordered events: %+v", got)
	}
}

func TestDigest_StableAndNonReversible(t *testing.T) {
	a := trajectory.Digest("secret-token-value")
	b := trajectory.Digest("secret-token-value")
	if a != b {
		t.Errorf("Digest is not stable: %q != %q", a, b)
	}
	if strings.Contains(a, "secret") {
		t.Errorf("Digest leaked its input: %q", a)
	}
	if !strings.HasPrefix(a, "sha256:") {
		t.Errorf("Digest = %q, want a sha256: prefix", a)
	}
	if a == trajectory.Digest("secret-token-valuf") {
		t.Error("Digest collided on a one-character change")
	}
}

func TestDigest_EmptyIsEmpty(t *testing.T) {
	// An absent value must digest to "" so omitempty can drop it, rather than to
	// the hash of the empty string — which would look like real content.
	if got := trajectory.Digest(""); got != "" {
		t.Errorf("Digest(\"\") = %q, want \"\"", got)
	}
}
