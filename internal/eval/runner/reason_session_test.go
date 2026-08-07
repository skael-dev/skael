package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/sandbox"
)

// The reason a task failed is the single most useful thing an eval produces
// and it used to be discarded: RunSpec.Stdout was nil at the verifier call
// site, so only the exit code survived.
func TestExecute_RecordsTheVerifiersFailureReason(t *testing.T) {
	h := newHarness(t)
	h.driver.result = func(rs sandbox.RunSpec) (sandbox.RunResult, error) {
		if !strings.Contains(strings.Join(rs.Argv, " "), "verifier/test.sh") {
			return sandbox.RunResult{ExitCode: 0}, nil
		}
		if rs.Stdout != nil {
			_, _ = rs.Stdout.Write([]byte("checking\nFAIL: row_count should be 2\n"))
		}
		return sandbox.RunResult{ExitCode: 1}, nil
	}

	res, err := h.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var seen bool
	for _, o := range res.Outcomes {
		if o.Key.Condition != runner.CondSkill {
			continue
		}
		if o.Reason != "" {
			seen = true
			if o.Reason != "row_count should be 2" {
				t.Errorf("Reason = %q, want %q", o.Reason, "row_count should be 2")
			}
		}
	}
	if !seen {
		t.Fatal("no outcome carried a reason; the verifier's output was discarded")
	}
}

func TestExecute_APassingVerifierRecordsNoReason(t *testing.T) {
	h := newHarness(t)
	h.driver.result = func(rs sandbox.RunSpec) (sandbox.RunResult, error) {
		if rs.Stdout != nil {
			_, _ = rs.Stdout.Write([]byte("all good\n"))
		}
		return sandbox.RunResult{ExitCode: 0}, nil
	}

	res, err := h.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range res.Outcomes {
		if o.Reason != "" {
			t.Errorf("a passing run recorded a reason: %q", o.Reason)
		}
	}
}
