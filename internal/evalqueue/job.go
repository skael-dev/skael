// Package evalqueue is the eval job queue. Postgres is the queue: workers
// claim with SELECT … FOR UPDATE SKIP LOCKED, hold a lease with a heartbeat,
// and a lapsed lease returns the job to the pool. No new infrastructure — a
// worker is one more container in the compose file.
package evalqueue

import (
	"context"
	"errors"
	"time"
)

// JobID identifies an eval job.
type JobID string

// Status is the lifecycle state of a job.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusDone      Status = "done"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Panel is the requested agent/model panel for a run. Empty means the
// worker's default panel.
type Panel struct {
	Agents []string `json:"agents,omitempty"`
	Models []string `json:"models,omitempty"`
}

// Job is a unit of eval work against a specific skill version.
type Job struct {
	ID             JobID
	SkillID        string
	SkillName      string
	Version        int
	SuiteRef       string
	Tier           string
	Panel          Panel
	Status         Status
	Attempts       int
	MaxAttempts    int
	WorkerID       string
	LeaseExpiresAt *time.Time
	// LeaseSeconds is the lease duration granted at claim time, persisted so
	// a heartbeat can re-apply it without the worker resending it and
	// without the server guessing at a fixed value.
	LeaseSeconds int
	LastError    string
	RequestedBy  string
	CreatedAt    time.Time
	// StartedAt is when the job first entered `running`. It is set once and
	// never moved: a retry re-claims the same job and elapsed time is
	// measured from when the work began, not from the latest attempt.
	// Elapsed cannot be derived from LeaseExpiresAt, which every heartbeat
	// pushes forward.
	StartedAt *time.Time
}

// Executor submits and cancels eval jobs.
type Executor interface {
	Submit(ctx context.Context, j Job) (JobID, error)
	Cancel(ctx context.Context, id JobID) error
}

// ErrNotCancellable is returned when Cancel targets a job that has already
// finished (done, failed, or cancelled) or does not exist.
var ErrNotCancellable = errors.New("evalqueue: job is not queued or running")
