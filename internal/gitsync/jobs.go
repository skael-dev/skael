package gitsync

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// SyncInterval is how often every connection is compared with its branch. A
// webhook only brings that comparison forward.
const SyncInterval = time.Minute

// JobInserter enqueues a sync.
type JobInserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// SyncArgs asks for one connection's sync. Args hold ids only, because River UI
// shows them to admins.
type SyncArgs struct {
	ConnectionID string `json:"connection_id"`
	Force        bool   `json:"force,omitempty"`
}

func (SyncArgs) Kind() string { return "git_sync" }

// InsertOpts collapses a burst of webhooks and ticks into one sync per
// connection. River requires the running state in the set, so a push during a
// running sync waits for the next tick, at most SyncInterval later.
func (SyncArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 5,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending,
				rivertype.JobStateRunning, rivertype.JobStateScheduled, rivertype.JobStateRetryable},
		},
	}
}

// SyncWorker runs one connection's sync.
type SyncWorker struct {
	river.WorkerDefaults[SyncArgs]
	Syncer *Syncer
}

// Work syncs. River retries an error; an unreachable repo is not an error but
// an errored connection.
func (w *SyncWorker) Work(ctx context.Context, job *river.Job[SyncArgs]) error {
	return w.Syncer.Reconcile(ctx, job.Args.ConnectionID, job.Args.Force)
}

// Timeout bounds one sync: a large repo downloads and every changed folder
// goes through the scan.
func (w *SyncWorker) Timeout(*river.Job[SyncArgs]) time.Duration { return 10 * time.Minute }

// SyncAllArgs is the periodic tick that enqueues every connection's sync.
type SyncAllArgs struct{}

func (SyncAllArgs) Kind() string { return "git_sync_all" }

// SyncAllWorker enqueues a sync for every connection.
type SyncAllWorker struct {
	river.WorkerDefaults[SyncAllArgs]
	Store *Store
}

// Work enqueues through the client running it, so the tick needs no handle of
// its own.
func (w *SyncAllWorker) Work(ctx context.Context, _ *river.Job[SyncAllArgs]) error {
	ids, err := w.Store.ConnectionIDs(ctx)
	if err != nil {
		return err
	}
	client := river.ClientFromContext[pgx.Tx](ctx)
	for _, id := range ids {
		if _, err := client.Insert(ctx, SyncArgs{ConnectionID: id}, nil); err != nil {
			return err
		}
	}
	return nil
}
