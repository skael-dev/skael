package whetstone

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// gradePlan builds a plan holding n evals, ids 1..n, each with one expectation.
func gradePlan(n int) *runner.Plan {
	p := &runner.Plan{}
	for i := 1; i <= n; i++ {
		p.Evals = append(p.Evals, suite.Eval{
			ID: i, Prompt: "do the thing", Expectations: []string{"it did the thing"},
		})
	}
	return p
}

func gradeOuts(n int) []runner.Outcome {
	var outs []runner.Outcome
	for i := 1; i <= n; i++ {
		outs = append(outs, runner.Outcome{
			Key:    store.RunKey{TaskID: runner.EvalID(i), Condition: runner.CondSkill, Attempt: 1},
			Status: store.StatusOK,
		})
	}
	return outs
}

const gradeReply = `{"expectations":[{"passed":true,"evidence":"it did"}]}`

// TestGradeOutcomes_OneFailedGradeDoesNotDiscardTheRun covers the defect where
// a single transient judge failure threw away every session of an eval.
func TestGradeOutcomes_OneFailedGradeDoesNotDiscardTheRun(t *testing.T) {
	var calls int32
	gw := fake.NewFunc(func(llm.Req) (string, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return "", errors.New("api: 429: rate limited")
		}
		return gradeReply, nil
	})
	g, err := score.NewGrader(gw)
	if err != nil {
		t.Fatalf("NewGrader: %v", err)
	}

	graded, dropped, err := gradeOutcomes(context.Background(), g, gradePlan(3), finishedOutcomes(gradeOuts(3)), 1)
	if err != nil {
		t.Fatalf("gradeOutcomes: %v", err)
	}
	if len(graded) != 2 {
		t.Errorf("graded %d runs, want 2", len(graded))
	}
	if len(dropped) != 1 {
		t.Fatalf("dropped %d runs, want 1", len(dropped))
	}
	if !strings.Contains(dropped[0].Reason, "429") {
		t.Errorf("dropped reason = %q, want the judge's error in it", dropped[0].Reason)
	}
}

// TestGradeOutcomes_ATotalJudgeOutageStillFails keeps the loud failure: a run
// with no grade at all is an error, not a zero score.
func TestGradeOutcomes_ATotalJudgeOutageStillFails(t *testing.T) {
	gw := fake.NewFunc(func(llm.Req) (string, error) {
		return "", errors.New("api: 500: judge unavailable")
	})
	g, err := score.NewGrader(gw)
	if err != nil {
		t.Fatalf("NewGrader: %v", err)
	}

	_, _, err = gradeOutcomes(context.Background(), g, gradePlan(2), finishedOutcomes(gradeOuts(2)), 1)
	if err == nil {
		t.Fatal("gradeOutcomes succeeded with every grade call failing")
	}
	if !strings.Contains(err.Error(), "judge") {
		t.Errorf("error = %q, want the judge named", err)
	}
}

// TestTruncateTranscript_KeepsAHeadATailAndAMarker pins the cap that keeps a
// long session from overflowing the judge's context for a non-retryable 400.
func TestTruncateTranscript_KeepsAHeadATailAndAMarker(t *testing.T) {
	head := strings.Repeat("H", transcriptHeadBytes)
	tail := strings.Repeat("T", transcriptTailBytes)
	in := head + strings.Repeat("M", 2<<20) + tail

	got := truncateTranscript(in)
	if !strings.HasPrefix(got, head) {
		t.Error("the head of the transcript was not kept")
	}
	if !strings.HasSuffix(got, tail) {
		t.Error("the tail of the transcript was not kept")
	}
	if strings.Contains(got, "M") {
		t.Error("the middle was kept")
	}
	if !strings.Contains(got, "truncated in the middle") {
		t.Error("no marker between the head and the tail")
	}

	short := strings.Repeat("x", 10)
	if truncateTranscript(short) != short {
		t.Error("a short transcript was altered")
	}
}

// TestRenderOutputs_StopsAtTheTotalCap covers the unbounded file count: the
// per-file cap bounds one file, not the block.
func TestRenderOutputs_StopsAtTheTotalCap(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		name := filepath.Join(root, fmt.Sprintf("f%02d.txt", i))
		if err := os.WriteFile(name, []byte(strings.Repeat("a", maxOutputBytes)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := renderOutputs(dir)
	if len(got) > maxOutputsBytes+maxOutputBytes+1024 {
		t.Errorf("outputs block is %d bytes, want it stopped near the %d byte cap", len(got), maxOutputsBytes)
	}
	if !strings.Contains(got, "total cap") {
		t.Error("the block does not say it was capped")
	}
}
