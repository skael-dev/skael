package evalqueue_test

import (
	"context"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/testutil"
)

func okp(v bool) *bool { return &v }

// The Jobs page reads progress from job events. A retried attempt starts a new
// plan, so its sessions count afresh, while the cost keeps every attempt.
func TestJobSummaries_ProgressCountsTheLatestAttempt(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	id, err := q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: 2, SuiteRef: "sha256:abcd"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := q.Claim(ctx, "w1", time.Minute); err != nil || !ok {
		t.Fatalf("claim: ok %v err %v", ok, err)
	}
	if err := q.AppendEvents(ctx, id, []evalqueue.EvalJobEvent{
		{Kind: evalqueue.EventPlan, Total: 3},
		{Kind: evalqueue.EventSessionEnd, OK: okp(false), CostUSD: 0.25, Error: "boom"},
		{Kind: evalqueue.EventPlan, Total: 3},
		{Kind: evalqueue.EventPhase, Phase: "sessions"},
		{Kind: evalqueue.EventSessionEnd, OK: okp(true), CostUSD: 0.5},
		{Kind: evalqueue.EventSessionEnd, OK: okp(false), CostUSD: 0.25, Error: "model not found"},
	}); err != nil {
		t.Fatal(err)
	}

	jobs, err := q.JobSummaries(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	j := jobs[0]
	if j.Status != "running" || j.WorkerID != "w1" || j.Phase != "sessions" || j.Total != 3 || j.Done != 2 || j.Failed != 1 {
		t.Errorf("summary = %+v", j)
	}
	if j.CostUSD != 1.0 {
		t.Errorf("cost = %v, want every attempt's 1.0", j.CostUSD)
	}
	if j.Stalled || j.LastEventAt == nil {
		t.Errorf("a job with a fresh event is not stalled: %+v", j)
	}

	later, err := q.JobSummaries(ctx, time.Now().Add(evalqueue.StalledAfter+time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !later[0].Stalled {
		t.Error("a running job with no event for longer than StalledAfter must show as stalled")
	}

	events, err := q.Events(ctx, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 6 || events[5].Error != "model not found" || events[5].OK == nil || *events[5].OK {
		t.Fatalf("events = %+v", events)
	}
	if after, _ := q.Events(ctx, id, events[3].ID); len(after) != 2 {
		t.Errorf("events after %d = %d, want 2", events[3].ID, len(after))
	}
}

// A finished job keeps its row, and its events go after EventRetention.
func TestPruneEvents_DropsOnlyOldFinishedJobs(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	old, _ := q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: 1, SuiteRef: "sha256:a"})
	live, _ := q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: 2, SuiteRef: "sha256:a"})
	for _, id := range []evalqueue.JobID{old, live} {
		if err := q.AppendEvents(ctx, id, []evalqueue.EvalJobEvent{{Kind: evalqueue.EventPhase, Phase: "fetching"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.Cancel(ctx, old); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE eval_jobs SET updated_at = now() - interval '31 days' WHERE id = $1`, string(old)); err != nil {
		t.Fatal(err)
	}
	n, err := q.PruneEvents(ctx)
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, err %v; want 1", n, err)
	}
	if got, _ := q.Get(ctx, old); got == nil {
		t.Error("pruning removed the job row")
	}
	if ev, _ := q.Events(ctx, live, 0); len(ev) != 1 {
		t.Errorf("a queued job lost its events: %d", len(ev))
	}
}
