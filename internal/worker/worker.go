// Package worker implements the eval queue worker's run loop: claim a job
// from the server, materialise a local whetstone workspace from the
// downloaded skill bundle and suite, run the evaluation, and post the report
// back. The Runner interface abstracts the actual eval execution (Docker and
// all) out of this package, so the loop itself — claiming, heartbeating,
// abandoning a lost lease, reporting a failure — is fully testable without
// Docker or a network.
package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
)

// Config configures a Worker.
type Config struct {
	Endpoint string
	APIKey   string
	WorkerID string

	Lease        time.Duration // default 5m
	Heartbeat    time.Duration // default Lease/3
	PollInterval time.Duration // default 15s
	WorkRoot     string        // scratch; default os.TempDir()
	Tier         string        // default from the job
}

const (
	defaultLease        = 5 * time.Minute
	defaultPollInterval = 15 * time.Second
)

func (c Config) withDefaults() Config {
	if c.Lease <= 0 {
		c.Lease = defaultLease
	}
	if c.Heartbeat <= 0 {
		c.Heartbeat = c.Lease / 3
	}
	if c.PollInterval <= 0 {
		c.PollInterval = defaultPollInterval
	}
	if c.WorkRoot == "" {
		c.WorkRoot = os.TempDir()
	}
	return c
}

// RunInput is what a Runner needs to execute one job's evaluation.
type RunInput struct {
	JobID    evalqueue.JobID
	Skill    string
	Version  int
	SuiteRef string
	Tier     string
	Panel    evalqueue.Panel

	// WorkspaceDir already contains an initialised whetstone workspace with
	// the skill bundle, the suite, and the suite's recorded checks.
	WorkspaceDir string
}

// Runner is the eval half, injected so the loop is testable with no Docker.
type Runner interface {
	Run(ctx context.Context, in RunInput) (*report.Report, error)
}

// SuiteMeta is everything about a stored suite the worker needs besides the
// archive bytes themselves: the oracle-gate checks (without which
// RunEvalWith's gate refuses to run at all) and the spec it was checked
// against (nil if none was recorded — see MaterializeInput.Spec).
type SuiteMeta struct {
	Checks []evalsuite.Check
	Spec   *spec.SkillSpec
}

// API is the server surface the worker needs, so a test can fake it.
type API interface {
	Claim(ctx context.Context, workerID string, lease time.Duration) (*evalqueue.Job, string, bool, error)
	Heartbeat(ctx context.Context, id evalqueue.JobID, token string) error
	PostReport(ctx context.Context, id evalqueue.JobID, token string, r *report.Report) error
	FailJob(ctx context.Context, id evalqueue.JobID, token, cause string) error
	FetchSuite(ctx context.Context, ref string) ([]byte, error)
	FetchBundle(ctx context.Context, skill string, version int) ([]byte, error)
	SuiteMeta(ctx context.Context, ref string) (SuiteMeta, error)
}

// Worker runs the claim/materialise/evaluate/report loop.
type Worker struct {
	cfg    Config
	api    API
	runner Runner
}

// New builds a Worker. cfg's zero-value fields are filled with defaults.
// WorkRoot is created here (if missing) so a bad path fails at startup
// rather than on every subsequent job.
func New(cfg Config, api API, r Runner) (*Worker, error) {
	if api == nil {
		return nil, errors.New("worker: New requires a non-nil API")
	}
	if r == nil {
		return nil, errors.New("worker: New requires a non-nil Runner")
	}
	cfg = cfg.withDefaults()
	if err := os.MkdirAll(cfg.WorkRoot, 0o755); err != nil {
		return nil, fmt.Errorf("worker: New: WorkRoot %q: %w", cfg.WorkRoot, err)
	}
	return &Worker{cfg: cfg, api: api, runner: r}, nil
}

// Loop calls RunOnce until ctx is cancelled, sleeping PollInterval when idle.
func (w *Worker) Loop(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		worked, err := w.RunOnce(ctx)
		if err != nil {
			log.Error().Err(err).Msg("worker: run failed")
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(w.cfg.PollInterval):
		}
	}
}

// RunOnce claims at most one job and runs it to completion. It returns
// (false, nil) when the queue is empty.
func (w *Worker) RunOnce(ctx context.Context) (worked bool, err error) {
	job, token, ok, err := w.api.Claim(ctx, w.cfg.WorkerID, w.cfg.Lease)
	if err != nil {
		return false, fmt.Errorf("worker: claim: %w", err)
	}
	if !ok {
		return false, nil
	}

	if runErr := w.runJob(ctx, job, token); runErr != nil {
		if failErr := w.api.FailJob(ctx, job.ID, token, runErr.Error()); failErr != nil {
			log.Error().Err(failErr).Str("job_id", string(job.ID)).Msg("worker: failed to report job failure")
		}
		return true, runErr
	}
	return true, nil
}

// runJob materialises a workspace for job, runs it through the Runner while
// heartbeating the lease, and posts the resulting report. It never calls
// FailJob or PostReport itself — RunOnce owns reporting the outcome, because
// the abandoned-lease path must post neither.
func (w *Worker) runJob(ctx context.Context, job *evalqueue.Job, token string) error {
	workDir, err := os.MkdirTemp(w.cfg.WorkRoot, "skael-eval-*")
	if err != nil {
		return fmt.Errorf("worker: create workspace: %w", err)
	}
	defer os.RemoveAll(workDir)

	bundle, err := w.api.FetchBundle(ctx, job.SkillName, job.Version)
	if err != nil {
		return fmt.Errorf("worker: fetch bundle: %w", err)
	}
	suiteArchive, err := w.api.FetchSuite(ctx, job.SuiteRef)
	if err != nil {
		return fmt.Errorf("worker: fetch suite: %w", err)
	}
	meta, err := w.api.SuiteMeta(ctx, job.SuiteRef)
	if err != nil {
		return fmt.Errorf("worker: fetch suite meta: %w", err)
	}

	st, err := Materialize(workDir, MaterializeInput{
		Skill: job.SkillName, Bundle: bundle, SuiteArchive: suiteArchive,
		Checks: meta.Checks, Spec: meta.Spec, WantSuiteRef: job.SuiteRef,
	})
	if err != nil {
		return fmt.Errorf("worker: materialize workspace: %w", err)
	}
	defer st.Close()

	tier := job.Tier
	if tier == "" {
		tier = w.cfg.Tier
	}

	// The heartbeat goroutine shares runCtx with the run: on ErrLeaseLost it
	// cancels runCtx so the run is abandoned rather than left to finish and
	// have its report posted against a claim that is no longer ours — by
	// then another worker owns the job.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	leaseLost := make(chan struct{})
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go func() {
		defer hbWG.Done()
		w.heartbeatLoop(ctx, runCtx, cancel, job.ID, token, leaseLost)
	}()

	rep, runErr := w.runner.Run(runCtx, RunInput{
		JobID: job.ID,
		Skill: job.SkillName, Version: job.Version, SuiteRef: job.SuiteRef,
		Tier: tier, Panel: job.Panel, WorkspaceDir: workDir,
	})

	cancel()
	hbWG.Wait()

	select {
	case <-leaseLost:
		return fmt.Errorf("worker: lease lost; run abandoned")
	default:
	}

	if runErr != nil {
		return fmt.Errorf("worker: run: %w", runErr)
	}
	if rep == nil {
		return fmt.Errorf("worker: run returned no report and no error")
	}
	if rep.SuiteRef != job.SuiteRef {
		return fmt.Errorf("worker: report suite_ref %q does not match job suite_ref %q", rep.SuiteRef, job.SuiteRef)
	}

	if err := w.api.PostReport(ctx, job.ID, token, rep); err != nil {
		return fmt.Errorf("worker: post report: %w", err)
	}
	return nil
}

// heartbeatLoop extends job's lease every Heartbeat interval until runCtx is
// cancelled (the run finished) or the lease is lost, in which case it
// cancels runCtx itself and closes leaseLost so runJob knows not to post.
func (w *Worker) heartbeatLoop(parent, runCtx context.Context, cancel context.CancelFunc, id evalqueue.JobID, token string, leaseLost chan struct{}) {
	ticker := time.NewTicker(w.cfg.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-runCtx.Done():
			return
		case <-ticker.C:
			if err := w.api.Heartbeat(parent, id, token); err != nil {
				if errors.Is(err, evalqueue.ErrLeaseLost) {
					close(leaseLost)
					cancel()
					return
				}
				log.Error().Err(err).Str("job_id", string(id)).Msg("worker: heartbeat failed")
			}
		}
	}
}
