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

	// AllowVoid excludes void tasks instead of refusing the run. Sourced from
	// the suite's own recorded origin (evalsuite.Origin), not from whether
	// this run derived the suite: a derived suite asks for 18 tasks precisely
	// so the ones its own oracle gate voids are absorbed, and there is no
	// author to go repair them — that stays true on a retry or any later run
	// against an already-derived suite. An authored suite keeps the stricter
	// contract regardless of which run is asking.
	AllowVoid bool

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
	// Origin is how the suite came to exist. It is the source of truth for
	// void-tolerance (RunInput.AllowVoid): a property of the suite, not of
	// which run happened to derive it — a retry or any later run against an
	// already-derived suite must still tolerate its voids.
	Origin evalsuite.Origin
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

// PushSuiteInput is one derived-suite upload. JobID and ClaimToken are the
// worker's proof that it holds the claim on a job with no suite of its own,
// which is what lets the server record the suite as derived at push time
// rather than trusting a worker-declared origin.
type PushSuiteInput struct {
	Skill      string
	Archive    []byte
	Checks     []evalsuite.Check
	Spec       *spec.SkillSpec
	JobID      evalqueue.JobID
	ClaimToken string
}

// DeriveInput is what a Deriver needs to build a suite for one job. Tier and
// Panel travel with it because the derived suite is gated by dry-running the
// real planner against them.
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

// Deriver builds a suite for a skill that has none. Injected for the same
// reason Runner is: this package must stay testable with no LLM and no
// Docker.
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

// New builds a Worker. cfg's zero-value fields are filled with defaults.
// WorkRoot is created here (if missing) so a bad path fails at startup
// rather than on every subsequent job. deriver may be nil — a worker
// deployed without derivation still drains jobs that already name a suite;
// it only fails a job that needs one.
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

	// The heartbeat goroutine shares runCtx with the run: on ErrLeaseLost it
	// cancels runCtx so the run is abandoned rather than left to finish and
	// have its report posted against a claim that is no longer ours — by
	// then another worker owns the job.
	//
	// Started here, before a single byte is fetched, rather than after
	// Materialize: deriving a suite is two LLM calls plus 18 tasks × 3
	// sandbox runs — minutes, comfortably past the 5-minute default
	// WORKER_LEASE. Left where it used to sit, the lease could lapse mid
	// derivation and strand the job.
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
		// No suite was registered when this job was submitted. Derive one,
		// push it, and continue down the ordinary path: re-downloading what
		// we just uploaded costs a round trip and keeps exactly one
		// materialization path, including Materialize's own ref check.
		if w.deriver == nil {
			return fmt.Errorf("worker: job %s has no suite_ref and this worker has no deriver configured", job.ID)
		}
		res, err := w.deriver.Derive(runCtx, DeriveInput{
			Skill: job.SkillName, Bundle: bundle, Tier: job.Tier, Panel: job.Panel,
		})
		if err != nil {
			return fmt.Errorf("worker: derive suite for %s: %w", job.SkillName, err)
		}
		// job.ID and token travel with the push so the server can attribute it
		// to the claim in flight and stamp the suite derived there and then. A
		// suite that only becomes derived when the report lands is authored
		// for as long as the run takes, and stays authored forever if the run
		// never reports — a machine-generated suite that a later re-run could
		// then use to clear a scan hold.
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
		// suiteRef, not job.SuiteRef: FetchSuite is a separate round trip
		// from whichever call produced suiteRef (PushSuite on the derive
		// path, the claimed job otherwise), and this is what verifies that
		// round trip actually delivered the same content — not an echo of
		// the value that produced it. job.SuiteRef is empty on the derive
		// path and would silently disable the check for every derived job.
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
		// Sourced from the suite's own recorded origin, not from whether this
		// run was the one that derived it: a retry or any later run against
		// an already-derived suite (job.SuiteRef non-empty from the start)
		// must still tolerate the voids that suite was built to absorb.
		AllowVoid: meta.Origin == evalsuite.OriginDerived,
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
