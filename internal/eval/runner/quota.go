package runner

import (
	"context"
	"sync"
	"time"
)

// quotaHoldThreshold is the reported utilization above which the gate holds
// new session starts. Above it the account is close enough to its window that
// starting another session mostly buys a rate-limited retry.
const quotaHoldThreshold = 0.95

// quotaGate holds new session starts while the account is near its window.
//
// A throttled session sleeps 30s, then 60s, then 120s, and re-runs from turn
// one, while the other slots keep pushing sessions into the same limit. The
// gate shares one signal across the run: a session already in flight is never
// interrupted, and only a start waits.
type quotaGate struct {
	now   func() time.Time
	sleep func(time.Duration)

	mu       sync.Mutex
	holdTill time.Time
}

func newQuotaGate(now func() time.Time, sleep func(time.Duration)) *quotaGate {
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = time.Sleep
	}
	return &quotaGate{now: now, sleep: sleep}
}

// Report records an observed utilization. Above the threshold it holds new
// starts for d.
func (g *quotaGate) Report(utilization float64, d time.Duration) {
	if utilization < quotaHoldThreshold || d <= 0 {
		return
	}
	until := g.now().Add(d)
	g.mu.Lock()
	defer g.mu.Unlock()
	if until.After(g.holdTill) {
		g.holdTill = until
	}
}

// Wait blocks until any hold has passed, or ctx is done. It reports how long
// it waited, which is what a test asserts rather than a wall clock.
func (g *quotaGate) Wait(ctx context.Context) time.Duration {
	var waited time.Duration
	for {
		g.mu.Lock()
		till := g.holdTill
		g.mu.Unlock()

		remaining := till.Sub(g.now())
		if remaining <= 0 || ctx.Err() != nil {
			return waited
		}
		g.sleep(remaining)
		waited += remaining
	}
}
