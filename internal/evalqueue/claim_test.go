package evalqueue_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/testutil"
)

var ctx = context.Background()

func TestClaim_TwoWorkersDoNotGetTheSameJob(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	_, _ = q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: 1, SuiteRef: "r"})

	j1, tok1, ok1, err := q.Claim(ctx, "worker-a", time.Minute)
	if err != nil || !ok1 {
		t.Fatalf("first claim: ok=%v err=%v", ok1, err)
	}
	_, _, ok2, err := q.Claim(ctx, "worker-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ok2 {
		t.Fatal("second worker claimed a job already leased")
	}
	if tok1 == "" {
		t.Fatal("claim returned an empty token")
	}
	if j1.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", j1.Attempts)
	}
}

func TestClaim_LapsedLeaseReturnsTheJob(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	id, _ := q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: 1, SuiteRef: "r"})

	// A zero lease is already expired the moment it is taken.
	if _, _, ok, _ := q.Claim(ctx, "worker-a", 0); !ok {
		t.Fatal("first claim failed")
	}
	j2, _, ok, err := q.Claim(ctx, "worker-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("a lapsed lease did not return the job to the pool")
	}
	if j2.ID != id || j2.Attempts != 2 {
		t.Fatalf("job = %s attempts = %d, want %s and 2", j2.ID, j2.Attempts, id)
	}
}

func TestHeartbeat_LostAfterAnotherWorkerReclaims(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	id, _ := q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: 1, SuiteRef: "r"})
	_, _, _, _ = q.Claim(ctx, "worker-a", 0)
	_, _, _, _ = q.Claim(ctx, "worker-b", time.Minute)

	if err := q.Heartbeat(ctx, id, "worker-a", time.Minute); !errors.Is(err, evalqueue.ErrLeaseLost) {
		t.Fatalf("heartbeat err = %v, want ErrLeaseLost", err)
	}
	if err := q.Heartbeat(ctx, id, "worker-b", time.Minute); err != nil {
		t.Fatalf("current owner's heartbeat failed: %v", err)
	}
}

func TestFail_RetriesUntilMaxAttemptsThenGivesUp(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	id, _ := q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: 1, SuiteRef: "r"})

	for i := 1; i <= 3; i++ {
		if _, _, ok, _ := q.Claim(ctx, "worker-a", time.Minute); !ok {
			t.Fatalf("claim %d failed", i)
		}
		if err := q.Fail(ctx, id, "worker-a", "sandbox unavailable"); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := q.Get(ctx, id)
	if got.Status != evalqueue.StatusFailed {
		t.Fatalf("status = %q after 3 attempts, want failed", got.Status)
	}
	if got.LastError == "" {
		t.Fatal("last_error is empty; the cause was lost")
	}
	if _, _, ok, _ := q.Claim(ctx, "worker-b", time.Minute); ok {
		t.Fatal("a permanently failed job was claimable")
	}
}

func TestVerifyClaim_RejectsAStaleToken(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	id, _ := q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: 1, SuiteRef: "r"})
	_, tok, _, _ := q.Claim(ctx, "worker-a", 0)
	_, _, _, _ = q.Claim(ctx, "worker-b", time.Minute) // reclaims, mints a new token

	if _, ok, err := q.VerifyClaim(ctx, id, tok); err != nil || ok {
		t.Fatalf("stale token accepted (ok=%v err=%v)", ok, err)
	}
	if _, ok, _ := q.VerifyClaim(ctx, id, "not-a-token"); ok {
		t.Fatal("a garbage token was accepted")
	}
}

func TestClaim_ConcurrentWorkersClaimDistinctJobs(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	const jobs = 8
	for i := 0; i < jobs; i++ {
		_, _ = q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: i + 1, SuiteRef: "r"})
	}
	var mu sync.Mutex
	seen := map[evalqueue.JobID]int{}
	var wg sync.WaitGroup
	for w := 0; w < jobs; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			j, _, ok, err := q.Claim(context.Background(), fmt.Sprintf("worker-%d", w), time.Minute)
			if err != nil || !ok {
				return
			}
			mu.Lock()
			seen[j.ID]++
			mu.Unlock()
		}(w)
	}
	wg.Wait()
	if len(seen) != jobs {
		t.Fatalf("claimed %d distinct jobs, want %d", len(seen), jobs)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("job %s claimed %d times", id, n)
		}
	}
}
