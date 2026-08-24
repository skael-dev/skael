// Package worker implements the eval queue worker's run loop: claim, materialise,
// evaluate, report. Runner abstracts Docker so the loop is testable.
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

	// AllowVoid excludes void tasks instead of refusing. Sourced from the
	// suite's machine_generated flag, not origin — an unreviewed authored
	// push is derived too, and reading origin would hand void tolerance to
	// a half-broken authored suite.
	AllowVoid    bool
	WorkspaceDir string
}

// Runner is the eval half, injected so the loop is testable with no Docker.
type Runner interface {
	Run(ctx context.Context, in RunInput) (*report.Report, error)
}

// SuiteMeta is the suite metadata the worker needs beside the archive bytes.
type SuiteMeta struct {
	Checks           []evalsuite.Check
	Spec             *spec.SkillSpec
	Origin           evalsuite.Origin
	MachineGenerated bool
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
	PushSuite(ctx context.Context, in PushSuiteInput) (string, error)
}

// PushSuiteInput is one derived-suite upload.
type PushSuiteInput struct {
	Skill      string
	Archive    []byte
	Checks     []evalsuite.Check
	Spec       *spec.SkillSpec
	JobID      evalqueue.JobID
	ClaimToken string
}

// DeriveInput is what a Deriver needs to build a suite for one job.
type DeriveInput struct {
	Skill  string
	Bundle []byte
	Tier   string
	Panel  evalqueue.Panel
}

// DeriveResult is a suite ready to push to the registry.
type DeriveResult struct {
	Archive []byte
	Checks  []evalsuite.Check
	Spec    *spec.SkillSpec
}

// Deriver builds a suite for a skill that has none.
type Deriver interface {
	Derive(ctx context.Context, in DeriveInput) (*DeriveResult, error)
}

// Worker runs the claim/materialise/evaluate/report loop.
type Worker struct {
	cfg     Config
	api     API
	runner  Runner
	deriver Deriver
}

// New builds a Worker. deriver may be nil; such a worker only handles jobs
// that already name a suite.
func New(cfg Config, api API, r Runner, deriver Deriver) (*Worker, error) {
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
	return &Worker{cfg: cfg, api: api, runner: r, deriver: deriver}, nil
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

// runJob materialises a workspace, runs the eval, and posts the report.
func (w *Worker) runJob(ctx context.Context, job *evalqueue.Job, token string) error {
	workDir, err := os.MkdirTemp(w.cfg.WorkRoot, "skael-eval-*")
	if err != nil {
		return fmt.Errorf("worker: create workspace: %w", err)
	}
	defer os.RemoveAll(workDir)

	// Heartbeat starts before fetching: derivation can take minutes.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	leaseLost := make(chan struct{})
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go func() {
		defer hbWG.Done()
		w.heartbeatLoop(ctx, runCtx, cancel, job.ID, token, leaseLost)
	}()

	bundle, err := w.api.FetchBundle(runCtx, job.SkillName, job.Version)
	if err != nil {
		return fmt.Errorf("worker: fetch bundle: %w", err)
	}

	suiteRef := job.SuiteRef
	if suiteRef == "" {
		if w.deriver == nil {
			return fmt.Errorf("worker: job %s has no suite_ref and this worker has no deriver configured", job.ID)
		}
		res, err := w.deriver.Derive(runCtx, DeriveInput{
			Skill: job.SkillName, Bundle: bundle, Tier: job.Tier, Panel: job.Panel,
		})
		if err != nil {
			return fmt.Errorf("worker: derive suite for %s: %w", job.SkillName, err)
		}
		ref, err := w.api.PushSuite(runCtx, PushSuiteInput{
			Skill: job.SkillName, Archive: res.Archive, Checks: res.Checks,
			Spec: res.Spec, JobID: job.ID, ClaimToken: token,
		})
		if err != nil {
			return fmt.Errorf("worker: push derived suite for %s: %w", job.SkillName, err)
		}
		log.Info().Str("job_id", string(job.ID)).Str("skill", job.SkillName).
			Str("suite_ref", ref).Int("tasks", len(res.Checks)).
			Msg("worker: derived a suite for a skill that had none")
		suiteRef = ref
	}

	suiteArchive, err := w.api.FetchSuite(runCtx, suiteRef)
	if err != nil {
		return fmt.Errorf("worker: fetch suite: %w", err)
	}
	meta, err := w.api.SuiteMeta(runCtx, suiteRef)
	if err != nil {
		return fmt.Errorf("worker: fetch suite meta: %w", err)
	}

	st, err := Materialize(workDir, MaterializeInput{
		Skill: job.SkillName, Bundle: bundle, SuiteArchive: suiteArchive,
		Checks: meta.Checks, Spec: meta.Spec,
		// suiteRef, not job.SuiteRef: the latter is empty on the derive path.
		WantSuiteRef: suiteRef,
	})
	if err != nil {
		return fmt.Errorf("worker: materialize workspace: %w", err)
	}
	defer st.Close()

	tier := job.Tier
	if tier == "" {
		tier = w.cfg.Tier
	}

	rep, runErr := w.runner.Run(runCtx, RunInput{
		JobID: job.ID,
		Skill: job.SkillName, Version: job.Version, SuiteRef: suiteRef,
		Tier: tier, Panel: job.Panel, WorkspaceDir: workDir,
		AllowVoid: meta.MachineGenerated,
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
	if rep.SuiteRef != suiteRef {
		return fmt.Errorf("worker: report suite_ref %q does not match job suite_ref %q", rep.SuiteRef, suiteRef)
	}

	if err := w.api.PostReport(ctx, job.ID, token, rep); err != nil {
		return fmt.Errorf("worker: post report: %w", err)
	}
	return nil
}

// heartbeatLoop extends the lease until the run finishes or the lease is lost.
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
