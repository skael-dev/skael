package evalqueue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Job event kinds a worker posts, plus cancelled, which the server records.
const (
	EventPhase        = "phase"
	EventHealth       = "health"
	EventPlan         = "plan"
	EventSessionStart = "session_start"
	EventSessionEnd   = "session_end"
	EventGrade        = "grade"
	EventCancelled    = "cancelled"
	EventFailed       = "failed"
)

// StalledAfter is how long a running job may go without a job event before it
// shows as stalled.
const StalledAfter = 3 * time.Minute

// EventRetention is how long a finished job keeps its events.
const EventRetention = 30 * 24 * time.Hour

// EvalJobEvent is one progress record of an eval job.
type EvalJobEvent struct {
	ID           int64     `json:"id"`
	At           time.Time `json:"at"`
	Kind         string    `json:"kind" maxLength:"32"`
	Phase        string    `json:"phase,omitempty" maxLength:"64"`
	Total        int       `json:"total,omitempty"`
	Task         string    `json:"task,omitempty" maxLength:"200"`
	Condition    string    `json:"condition,omitempty" maxLength:"32"`
	Model        string    `json:"model,omitempty" maxLength:"200"`
	Attempt      int       `json:"attempt,omitempty"`
	Status       string    `json:"status,omitempty" maxLength:"32"`
	OK           *bool     `json:"ok,omitempty"`
	DurationMS   int64     `json:"duration_ms,omitempty"`
	InputTokens  int64     `json:"input_tokens,omitempty"`
	OutputTokens int64     `json:"output_tokens,omitempty"`
	CostUSD      float64   `json:"cost_usd,omitempty"`
	Error        string    `json:"error,omitempty" maxLength:"4000"`
	Actor        string    `json:"actor,omitempty"`
}

// EvalJobSummary is one job on the Jobs page.
type EvalJobSummary struct {
	ID          string     `json:"id"`
	Skill       string     `json:"skill"`
	Version     int        `json:"version"`
	Tier        string     `json:"tier"`
	ContestID   string     `json:"contest_id,omitempty"`
	Status      string     `json:"status"`
	WorkerID    string     `json:"worker_id"`
	Attempts    int        `json:"attempts"`
	LastError   string     `json:"last_error"`
	RequestedBy string     `json:"requested_by"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Phase       string     `json:"phase"`
	Total       int        `json:"sessions_total"`
	Done        int        `json:"sessions_done"`
	Failed      int        `json:"sessions_failed"`
	CostUSD     float64    `json:"cost_usd"`
	LastEventAt *time.Time `json:"last_event_at,omitempty"`
	Stalled     bool       `json:"stalled"`
}

// AppendEvents stores events a worker posted for a job it holds.
func (p *PoolExecutor) AppendEvents(ctx context.Context, id JobID, events []EvalJobEvent) error {
	for _, e := range events {
		if err := p.appendEvent(ctx, id, e); err != nil {
			return err
		}
	}
	return nil
}

func (p *PoolExecutor) appendEvent(ctx context.Context, id JobID, e EvalJobEvent) error {
	at := e.At
	if at.IsZero() {
		at = time.Now()
	}
	kind := e.Kind
	e.ID, e.At, e.Kind = 0, time.Time{}, ""
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := p.db.Exec(ctx, `INSERT INTO eval_job_events (job_id, at, kind, data) VALUES ($1, $2, $3, $4)`,
		string(id), at, kind, data); err != nil {
		return fmt.Errorf("evalqueue: append event: %w", err)
	}
	return nil
}

// Events returns a job's events after the given id, oldest first.
func (p *PoolExecutor) Events(ctx context.Context, id JobID, after int64) ([]EvalJobEvent, error) {
	rows, err := p.db.Query(ctx, `SELECT id, at, kind, data FROM eval_job_events WHERE job_id = $1 AND id > $2 ORDER BY id LIMIT 2000`,
		string(id), after)
	if err != nil {
		return nil, fmt.Errorf("evalqueue: events: %w", err)
	}
	defer rows.Close()
	out := []EvalJobEvent{}
	for rows.Next() {
		var e EvalJobEvent
		var id int64
		var at time.Time
		var kind string
		var data []byte
		if err := rows.Scan(&id, &at, &kind, &data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, err
		}
		e.ID, e.At, e.Kind = id, at, kind
		out = append(out, e)
	}
	return out, rows.Err()
}

// JobSummaries lists every queued and running job, and jobs that finished in
// the last week, newest first, with their progress.
func (p *PoolExecutor) JobSummaries(ctx context.Context, now time.Time) ([]EvalJobSummary, error) {
	rows, err := p.db.Query(ctx, `
		SELECT j.id, j.skill_name, j.version, j.tier, COALESCE(j.contest_id::text, ''), j.status, j.worker_id, j.attempts,
			j.last_error, j.requested_by, j.created_at, j.started_at,
			CASE WHEN j.status IN ('done', 'failed', 'cancelled') THEN j.updated_at END,
			COALESCE((SELECT data->>'phase' FROM eval_job_events e WHERE e.job_id = j.id AND e.kind = 'phase' ORDER BY e.id DESC LIMIT 1), ''),
			COALESCE((SELECT (data->>'total')::int FROM eval_job_events e WHERE e.job_id = j.id AND e.kind = 'plan' ORDER BY e.id DESC LIMIT 1), 0),
			(SELECT count(*) FROM eval_job_events e WHERE e.job_id = j.id AND e.kind = 'session_end' AND e.id > lp.plan_id),
			(SELECT count(*) FROM eval_job_events e WHERE e.job_id = j.id AND e.kind = 'session_end' AND e.id > lp.plan_id AND e.data->>'ok' = 'false'),
			COALESCE((SELECT sum((data->>'cost_usd')::float8) FROM eval_job_events e WHERE e.job_id = j.id AND e.kind = 'session_end'), 0),
			(SELECT max(at) FROM eval_job_events e WHERE e.job_id = j.id)
		FROM eval_jobs j
		-- A retried job starts a new plan; its earlier attempt's sessions
		-- still count toward the cost, not toward this attempt's progress.
		LEFT JOIN LATERAL (SELECT COALESCE(max(id), 0) AS plan_id FROM eval_job_events
			WHERE job_id = j.id AND kind = 'plan') lp ON true
		WHERE j.status IN ('queued', 'running') OR j.updated_at > now() - make_interval(days => 7)
		ORDER BY j.created_at DESC
		LIMIT 200`)
	if err != nil {
		return nil, fmt.Errorf("evalqueue: job summaries: %w", err)
	}
	defer rows.Close()
	out := []EvalJobSummary{}
	for rows.Next() {
		var s EvalJobSummary
		var done, failed int64
		if err := rows.Scan(&s.ID, &s.Skill, &s.Version, &s.Tier, &s.ContestID, &s.Status, &s.WorkerID, &s.Attempts,
			&s.LastError, &s.RequestedBy, &s.CreatedAt, &s.StartedAt, &s.FinishedAt,
			&s.Phase, &s.Total, &done, &failed, &s.CostUSD, &s.LastEventAt); err != nil {
			return nil, err
		}
		s.Done, s.Failed = int(done), int(failed)
		s.Stalled = Stalled(s.Status, s.StartedAt, s.LastEventAt, now)
		out = append(out, s)
	}
	return out, rows.Err()
}

// Stalled reports whether a running job has gone StalledAfter without a job
// event, counting from its start when it has none.
func Stalled(status string, started, lastEvent *time.Time, now time.Time) bool {
	if status != string(StatusRunning) {
		return false
	}
	last := lastEvent
	if last == nil {
		last = started
	}
	return last != nil && now.Sub(*last) > StalledAfter
}

// PruneEvents deletes the events of jobs that finished before EventRetention.
func (p *PoolExecutor) PruneEvents(ctx context.Context) (int64, error) {
	tag, err := p.db.Exec(ctx, `
		DELETE FROM eval_job_events e USING eval_jobs j
		WHERE e.job_id = j.id AND j.status IN ('done', 'failed', 'cancelled')
		  AND j.updated_at < now() - make_interval(days => 30)`)
	if err != nil {
		return 0, fmt.Errorf("evalqueue: prune events: %w", err)
	}
	return tag.RowsAffected(), nil
}
