package server

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/rs/zerolog/log"
	"riverqueue.com/riverui"

	"github.com/skael-dev/skael/internal/analytics"
	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/sets"
	"github.com/skael-dev/skael/internal/webhooks"
)

// RiverUIPrefix is where River UI mounts. The auth middleware covers only /api/,
// and the route table holds no scope for it, so a service key never reaches it.
const RiverUIPrefix = "/api/admin/river"

// reapInterval is how often the eval queue is swept for jobs whose lease lapsed
// with no attempts remaining. A lease is usually minutes long, so this only
// needs to run more often than leases expire.
const reapInterval = time.Minute

// Job arguments hold ids only, because River UI shows them to admins. These two
// hold nothing at all. TestJobArgsHoldIDsOnly enforces it.
type evalLeaseReapArgs struct{}

func (evalLeaseReapArgs) Kind() string { return "eval_lease_reap" }

type eventRetentionArgs struct{}

func (eventRetentionArgs) Kind() string { return "event_retention" }

type evalLeaseReapWorker struct {
	river.WorkerDefaults[evalLeaseReapArgs]
	queue *evalqueue.PoolExecutor
}

// Work moves a running job with a lapsed lease and no attempts left to failed.
// Claim requires attempts < max_attempts, so nothing else would ever move it.
func (w *evalLeaseReapWorker) Work(ctx context.Context, _ *river.Job[evalLeaseReapArgs]) error {
	n, err := w.queue.ReapExpired(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Info().Int("reaped", n).Msg("eval queue: reaped jobs with lapsed leases and no attempts remaining")
	}
	return nil
}

type eventRetentionWorker struct {
	river.WorkerDefaults[eventRetentionArgs]
	store *analytics.Store
	days  int
}

func (w *eventRetentionWorker) Work(ctx context.Context, _ *river.Job[eventRetentionArgs]) error {
	deleted, err := w.store.CleanupOldEvents(ctx, w.days)
	if err != nil {
		return err
	}
	if deleted > 0 {
		log.Info().Int64("deleted", deleted).Int("retention_days", w.days).Msg("event retention cleanup complete")
	}
	return nil
}

// newRiverClient builds the client for all server background work. It touches
// no database until Start.
func newRiverClient(pool *pgxpool.Pool, cfg *platform.Config, analyticsStore *analytics.Store) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, webhooks.NewDeliveryWorker(webhooks.NewStore(pool), cfg.WebhookAllowPrivate))
	river.AddWorker(workers, &evalLeaseReapWorker{queue: evalqueue.NewPool(pool)})
	river.AddWorker(workers, &eventRetentionWorker{store: analyticsStore, days: cfg.EventRetentionDays})

	periodic := []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(reapInterval),
			func() (river.JobArgs, *river.InsertOpts) { return evalLeaseReapArgs{}, nil }, nil),
	}
	if cfg.EventRetentionDays > 0 {
		periodic = append(periodic, river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return eventRetentionArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true}))
	}

	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger:       slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Queues:       map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 20}},
		Workers:      workers,
		PeriodicJobs: periodic,
	})
}

func newRiverUI(client *river.Client[pgx.Tx]) (*riverui.Handler, error) {
	return riverui.NewHandler(&riverui.HandlerOpts{
		Endpoints: riverui.NewEndpoints(client, nil),
		Logger:    slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Prefix:    RiverUIPrefix,
	})
}

// privilegedOnly limits a handler to instance owners and admins.
func privilegedOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.UserFromContext(r.Context()).IsPrivileged() {
			http.Error(w, "owner or admin role required", http.StatusForbidden)
			return
		}
		if next == nil {
			http.Error(w, "River UI is not running", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NewNotifierForTest builds a notifier over an insert-only client, so a test
// can drive a change inside a transaction of its own.
func NewNotifierForTest(pool *pgxpool.Pool) *webhooks.Notifier {
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		panic(err)
	}
	return webhooks.NewNotifier(client, sets.NewStore(pool))
}
