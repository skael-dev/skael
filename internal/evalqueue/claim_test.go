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

	if err := q.Heartbeat(ctx, id, "worker-a"); !errors.Is(err, evalqueue.ErrLeaseLost) {
		t.Fatalf("heartbeat err = %v, want ErrLeaseLost", err)
	}
	if err := q.Heartbeat(ctx, id, "worker-b"); err != nil {
		t.Fatalf("current owner's heartbeat failed: %v", err)
	}
}

func TestHeartbeat_ReappliesTheClaimedLeaseNotAFixedDefault(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	id, _ := q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: 1, SuiteRef: "r"})

	j, _, ok, err := q.Claim(ctx, "worker-a", 10*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if j.LeaseSeconds != 600 {
		t.Fatalf("claimed lease_seconds = %d, want 600", j.LeaseSeconds)
	}

	if err := q.Heartbeat(ctx, id, "worker-a"); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	after, err := q.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.LeaseExpiresAt == nil {
		t.Fatal("lease_expires_at is nil after heartbeat")
	}
	// A heartbeat that truncated the lease to a fixed 60s default would put
	// lease_expires_at well under a minute out. The claimed 10-minute lease
	// should still be in force.
	if time.Until(*after.LeaseExpiresAt) < 5*time.Minute {
		t.Fatalf("lease_expires_at = %v (in %s), want close to 10 minutes out — the heartbeat truncated the lease",
			after.LeaseExpiresAt, time.Until(*after.LeaseExpiresAt))
	}

	// Because the lease is still live, a second worker must not be able to
	// claim the job out from under the first.
	_, _, claimedAgain, err := q.Claim(ctx, "worker-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimedAgain {
		t.Fatal("a second worker claimed a job whose heartbeated lease is still live")
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

func TestVerifyClaim_AcceptsTheLiveToken(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	id, _ := q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: 1, SuiteRef: "r"})
	_, tok, ok, err := q.Claim(ctx, "worker-a", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}

	j, ok, err := q.VerifyClaim(ctx, id, tok)
	if err != nil || !ok {
		t.Fatalf("live token rejected (ok=%v err=%v)", ok, err)
	}
	if j.ID != id {
		t.Fatalf("verified job = %s, want %s", j.ID, id)
	}
}

func TestVerifyClaim_RejectsAfterCompleteBlanksTheHash(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	id, _ := q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: 1, SuiteRef: "r"})
	_, tok, _, _ := q.Claim(ctx, "worker-a", time.Minute)
	if err := q.Complete(ctx, id, "worker-a"); err != nil {
		t.Fatal(err)
	}

	if _, ok, _ := q.VerifyClaim(ctx, id, tok); ok {
		t.Fatal("a token for a completed (blanked-hash) job was accepted")
	}
	if _, ok, _ := q.VerifyClaim(ctx, id, ""); ok {
		t.Fatal("an empty token matched the blanked hash")
	}
}

func TestVerifyClaim_RejectsALapsedLease(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	id, _ := q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: 1, SuiteRef: "r"})
	// A zero lease is already expired the moment it is taken, but nobody has
	// reclaimed it yet — the hash is still in the row.
	_, tok, ok, err := q.Claim(ctx, "worker-a", 0)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}

	if _, ok, err := q.VerifyClaim(ctx, id, tok); err != nil || ok {
		t.Fatalf("a lapsed-lease token was accepted (ok=%v err=%v)", ok, err)
	}
}

func TestVerifyClaim_RejectsACancelledJob(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	id, _ := q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: 1, SuiteRef: "r"})
	_, tok, ok, err := q.Claim(ctx, "worker-a", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := q.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := q.VerifyClaim(ctx, id, tok); err != nil || ok {
		t.Fatalf("a cancelled job's token was accepted (ok=%v err=%v)", ok, err)
	}
}

func TestReapExpired_FailsAJobThatExhaustedAttemptsAndLapsed(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	id, _ := q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: 1, SuiteRef: "r"})

	// Claim to exhaustion (default max_attempts=3), leaving the job running
	// with a lapsed lease after the final attempt — as if the worker died.
	for i := 1; i <= 3; i++ {
		if _, _, ok, _ := q.Claim(ctx, "worker-a", 0); !ok {
			t.Fatalf("claim %d failed", i)
		}
	}

	n, err := q.ReapExpired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reaped %d jobs, want 1", n)
	}
	got, _ := q.Get(ctx, id)
	if got.Status != evalqueue.StatusFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if got.LastError == "" {
		t.Fatal("last_error is empty; the reap reason was lost")
	}
}

func TestReapExpired_DoesNotTouchAJobWithRetriesRemaining(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	id, _ := q.Submit(ctx, evalqueue.Job{SkillID: skillID, SkillName: "deploy-helper", Version: 1, SuiteRef: "r"})

	// One claim with a lapsed lease; two attempts remain.
	if _, _, ok, _ := q.Claim(ctx, "worker-a", 0); !ok {
		t.Fatal("claim failed")
	}

	n, err := q.ReapExpired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("reaped %d jobs, want 0 — retries remained", n)
	}

	j, _, ok, err := q.Claim(ctx, "worker-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("a job with retries remaining was not claimable after ReapExpired")
	}
	if j.ID != id {
		t.Fatalf("claimed job = %s, want %s", j.ID, id)
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
	var claimErrs []error
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < jobs; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			j, _, ok, err := q.Claim(context.Background(), fmt.Sprintf("worker-%d", w), time.Minute)
			if err != nil {
				mu.Lock()
				claimErrs = append(claimErrs, err)
				mu.Unlock()
				return
			}
			if !ok {
				return
			}
			mu.Lock()
			seen[j.ID]++
			mu.Unlock()
		}(w)
	}
	close(start)
	wg.Wait()
	if len(claimErrs) > 0 {
		t.Fatalf("claim returned %d errors, want 0: %v", len(claimErrs), claimErrs)
	}
	if len(seen) != jobs {
		t.Fatalf("claimed %d distinct jobs, want %d", len(seen), jobs)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("job %s claimed %d times", id, n)
		}
	}
}
