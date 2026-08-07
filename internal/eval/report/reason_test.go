package report_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/report"
)

func TestConditionReport_ReasonRoundTrips(t *testing.T) {
	in := report.ConditionReport{Condition: "skill", Model: "opus", Passes: 0, Runs: 1,
		Reason: "row_count should be 2"}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"reason":"row_count should be 2"`) {
		t.Fatalf("reason not marshalled: %s", b)
	}

	var out report.ConditionReport
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Reason != in.Reason {
		t.Errorf("Reason = %q, want %q", out.Reason, in.Reason)
	}
}

// omitempty, so a report written before this field existed still decodes and
// a passing task does not carry an empty key.
func TestConditionReport_ReasonIsOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(report.ConditionReport{Condition: "skill", Model: "opus", Passes: 1, Runs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "reason") {
		t.Errorf("empty reason was marshalled: %s", b)
	}
}
