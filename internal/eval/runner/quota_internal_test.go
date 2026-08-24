package runner

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeClock is a clock the gate's sleep advances, so a hold is measured
// rather than waited out.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newFakeGate() (*quotaGate, *fakeClock) {
	c := &fakeClock{t: time.Unix(1700000000, 0)}
	return newQuotaGate(c.now, c.sleep), c
}

// TestQuotaGate_HoldsTheNextStartAboveTheThreshold is the whole point: above
// 0.95 the account is close enough to its window that another start mostly
// buys a rate-limited retry.
func TestQuotaGate_HoldsTheNextStartAboveTheThreshold(t *testing.T) {
	g, _ := newFakeGate()
	g.Report(0.97, 30*time.Second)

	if waited := g.Wait(context.Background()); waited != 30*time.Second {
		t.Errorf("the gate held the next start for %s, want 30s", waited)
	}
}

// TestQuotaGate_DoesNotHoldBelowTheThreshold keeps the ordinary run at full
// concurrency: a reported utilization under the threshold is telemetry, not a
// reason to idle the pool.
func TestQuotaGate_DoesNotHoldBelowTheThreshold(t *testing.T) {
	g, _ := newFakeGate()
	g.Report(0.80, 30*time.Second)

	if waited := g.Wait(context.Background()); waited != 0 {
		t.Errorf("the gate held a start for %s at 0.80 utilization, want no hold", waited)
	}
}

// TestQuotaGate_ReleasesTheHealthySessionWhenTheHoldEnds pins that a
// rate-limited session does not stall a healthy one past the hold it reported.
func TestQuotaGate_ReleasesTheHealthySessionWhenTheHoldEnds(t *testing.T) {
	g, c := newFakeGate()
	g.Report(0.99, 60*time.Second)

	if waited := g.Wait(context.Background()); waited != 60*time.Second {
		t.Fatalf("first start waited %s, want 60s", waited)
	}
	// The hold has passed; the next start goes straight through.
	if waited := g.Wait(context.Background()); waited != 0 {
		t.Errorf("a start after the hold waited %s, want 0", waited)
	}
	if c.now().Before(time.Unix(1700000060, 0)) {
		t.Error("the clock did not advance through the hold")
	}
}

// TestQuotaGate_TakesTheLongestHold covers concurrent reports: two sessions
// throttled at once must not shorten each other's hold.
func TestQuotaGate_TakesTheLongestHold(t *testing.T) {
	g, _ := newFakeGate()
	g.Report(0.99, 30*time.Second)
	g.Report(0.99, 120*time.Second)
	g.Report(0.99, 60*time.Second)

	if waited := g.Wait(context.Background()); waited != 120*time.Second {
		t.Errorf("waited %s, want the longest hold of 120s", waited)
	}
}

// TestQuotaGate_ACancelledContextEndsTheHold keeps a cancelled run from
// sleeping out a quota window.
func TestQuotaGate_ACancelledContextEndsTheHold(t *testing.T) {
	g, _ := newFakeGate()
	g.Report(0.99, 300*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waited := g.Wait(ctx); waited != 0 {
		t.Errorf("a cancelled run waited %s, want 0", waited)
	}
}
