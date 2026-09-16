// Package evalqueue is the eval job queue, backed by Postgres claim/lease/heartbeat.
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
	LeaseSeconds   int
	LastError      string
	RequestedBy    string
	// ContestID is set when this job runs a contest. The job's SkillID,
	// SkillName and Version then hold the first candidate, so every existing
	// query on eval_jobs stays correct, and Candidates carries all of them.
	// One job, because two jobs can reach two workers with different agent CLI
	// versions on different days — the drift a contest exists to remove.
	ContestID  string
	Candidates []Candidate
	CreatedAt  time.Time
	// StartedAt is set once on the first claim and never moved, so elapsed
	// time is measured from the start of work, not the latest retry.
	StartedAt *time.Time
}

// Candidate is one entry in a contest job.
type Candidate struct {
	SkillID   string `json:"skill_id"`
	SkillName string `json:"skill_name"`
	Version   int    `json:"version"`
	Label     string `json:"label"`
}

// Executor submits and cancels eval jobs.
type Executor interface {
	Submit(ctx context.Context, j Job) (JobID, error)
	Cancel(ctx context.Context, id JobID) error
}

// ErrNotCancellable is returned when Cancel targets a job that has already
// finished (done, failed, or cancelled) or does not exist.
var ErrNotCancellable = errors.New("evalqueue: job is not queued or running")
