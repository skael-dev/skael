package worker_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/worker"
)

// fixtureBundle packs a minimal skill bundle (just SKILL.md) into a tar.gz,
// the same shape FetchBundle returns.
func fixtureBundle(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	md := "---\nname: deploy-helper\ndescription: Deploys the thing.\n---\n\n# deploy-helper\n\nDeploys the thing.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatalf("fixtureBundle: write SKILL.md: %v", err)
	}
	archive, _, _, err := skill.Pack(dir)
	if err != nil {
		t.Fatalf("fixtureBundle: pack: %v", err)
	}
	return archive
}

// fixtureSuiteArchive packs a freshly written fixture suite (one task) into
// an archive, the same shape FetchSuite returns.
func fixtureSuiteArchive(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	writeFixtureSuite(t, dir)
	archive, err := evalsuite.PackDir(dir)
	if err != nil {
		t.Fatalf("fixtureSuiteArchive: %v", err)
	}
	return archive
}

// writeFixtureSuite writes a minimal suite tree to dir that suite.Load
// accepts: one task with a prompt, oracle, and verifier.
func writeFixtureSuite(t *testing.T, dir string) {
	t.Helper()
	s := &suite.Suite{
		Tasks: []suite.TaskPkg{
			{
				ID:       "t1",
				Kind:     "happy",
				Split:    "holdout",
				PromptMD: "# Task\n\nDo the thing.\n",
				Oracle:   "#!/bin/sh\necho ok\n",
				Verifier: "#!/bin/sh\nexit 0\n",
			},
		},
		Triggers: suite.TriggerSet{
			Positive: []string{"do the thing"},
			Negative: []string{"do something unrelated"},
		},
	}
	if err := s.Write(dir); err != nil {
		t.Fatalf("writeFixtureSuite: %v", err)
	}
}

// reportFixture builds a minimal report as a fake Runner would return it.
func reportFixture(skill, suiteRef string, headline float64) *report.Report {
	return &report.Report{
		SchemaVersion: 1,
		Skill:         skill,
		SpecVersion:   1,
		Tier:          "smoke",
		SuiteRef:      suiteRef,
		EngineVersion: "test",
		Headline:      headline,
		StartedAt:     time.Now(),
		FinishedAt:    time.Now(),
	}
}

// fakeAPI is a test double for worker.API: an in-memory job queue plus
// recording of what the worker posted back.
type fakeAPI struct {
	t *testing.T

	mu           sync.Mutex
	queue        []evalqueue.Job
	posted       *report.Report
	failedCause  string
	heartbeats   int
	heartbeatErr error

	bundle       []byte
	suiteArchive []byte
	checks       []evalsuite.Check
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	return &fakeAPI{
		t:            t,
		bundle:       fixtureBundle(t),
		suiteArchive: fixtureSuiteArchive(t),
		checks:       []evalsuite.Check{{TaskID: "t1", OK: true}},
	}
}

func (f *fakeAPI) enqueue(j evalqueue.Job) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, j)
}

func (f *fakeAPI) Claim(_ context.Context, _ string, _ time.Duration) (*evalqueue.Job, string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queue) == 0 {
		return nil, "", false, nil
	}
	j := f.queue[0]
	f.queue = f.queue[1:]
	return &j, "claim-token", true, nil
}

func (f *fakeAPI) Heartbeat(_ context.Context, _ evalqueue.JobID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heartbeats++
	return f.heartbeatErr
}

func (f *fakeAPI) PostReport(_ context.Context, _ evalqueue.JobID, _ string, r *report.Report) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posted = r
	return nil
}

func (f *fakeAPI) FailJob(_ context.Context, _ evalqueue.JobID, _, cause string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failedCause = cause
	return nil
}

func (f *fakeAPI) FetchSuite(_ context.Context, _ string) ([]byte, error) {
	return f.suiteArchive, nil
}

func (f *fakeAPI) FetchBundle(_ context.Context, _ string, _ int) ([]byte, error) {
	return f.bundle, nil
}

func (f *fakeAPI) SuiteChecks(_ context.Context, _ string) ([]evalsuite.Check, error) {
	return f.checks, nil
}

// fakeRunner is a test double for worker.Runner: no Docker, just a canned
// report/error, optionally gated behind a channel so a test can control how
// long a run stays "in flight".
type fakeRunner struct {
	report *report.Report
	err    error
	block  chan struct{}

	mu       sync.Mutex
	gotInput worker.RunInput
}

func (f *fakeRunner) Run(ctx context.Context, in worker.RunInput) (*report.Report, error) {
	f.mu.Lock()
	f.gotInput = in
	f.mu.Unlock()

	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.report, nil
}

func (f *fakeRunner) input() worker.RunInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotInput
}

func TestWorker_RunOnce_ClaimsRunsAndPostsAReport(t *testing.T) {
	api := newFakeAPI(t)
	api.enqueue(evalqueue.Job{ID: "job-1", SkillName: "deploy-helper", Version: 2, SuiteRef: "sha256:abc", Tier: "smoke"})
	runner := &fakeRunner{report: reportFixture("deploy-helper", "sha256:abc", 71)}

	w, err := worker.New(worker.Config{Endpoint: "http://x", APIKey: "k", WorkerID: "w1", WorkRoot: t.TempDir()}, api, runner)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := w.RunOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("RunOnce = (%v, %v)", worked, err)
	}
	if api.posted == nil {
		t.Fatal("no report was posted")
	}
	if api.posted.Headline != 71 {
		t.Fatalf("posted headline = %v", api.posted.Headline)
	}
	if runner.input().WorkspaceDir == "" {
		t.Fatal("the runner was handed no workspace")
	}
}

// A worker that dies quietly leaves the job leased until the lease lapses. A
// worker that fails loudly returns it now, with the reason recorded.
func TestWorker_RunOnce_ReportsAFailureInsteadOfSwallowingIt(t *testing.T) {
	api := newFakeAPI(t)
	api.enqueue(evalqueue.Job{ID: "job-1", SkillName: "deploy-helper", Version: 2, SuiteRef: "sha256:abc"})
	runner := &fakeRunner{err: errors.New("sandbox unavailable")}
	w, _ := worker.New(worker.Config{Endpoint: "http://x", APIKey: "k", WorkerID: "w1", WorkRoot: t.TempDir()}, api, runner)

	if _, err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce hid the run failure")
	}
	if api.failedCause == "" || !strings.Contains(api.failedCause, "sandbox unavailable") {
		t.Fatalf("failure cause reported = %q", api.failedCause)
	}
	if api.posted != nil {
		t.Fatal("a report was posted for a failed run")
	}
}

func TestWorker_RunOnce_EmptyQueueIsNotAnError(t *testing.T) {
	w, _ := worker.New(worker.Config{Endpoint: "http://x", APIKey: "k", WorkerID: "w1", WorkRoot: t.TempDir()},
		newFakeAPI(t), &fakeRunner{})
	worked, err := w.RunOnce(context.Background())
	if err != nil || worked {
		t.Fatalf("RunOnce on an empty queue = (%v, %v), want (false, nil)", worked, err)
	}
}

func TestWorker_HeartbeatsWhileTheRunIsInFlight(t *testing.T) {
	api := newFakeAPI(t)
	api.enqueue(evalqueue.Job{ID: "job-1", SkillName: "deploy-helper", Version: 1, SuiteRef: "sha256:abc"})
	release := make(chan struct{})
	runner := &fakeRunner{report: reportFixture("deploy-helper", "sha256:abc", 60), block: release}
	w, _ := worker.New(worker.Config{Endpoint: "http://x", APIKey: "k", WorkerID: "w1",
		WorkRoot: t.TempDir(), Lease: 300 * time.Millisecond, Heartbeat: 50 * time.Millisecond}, api, runner)

	done := make(chan error, 1)
	go func() { _, err := w.RunOnce(context.Background()); done <- err }()
	time.Sleep(200 * time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	heartbeats := api.heartbeats
	api.mu.Unlock()
	if heartbeats < 2 {
		t.Fatalf("heartbeats = %d during a run longer than the interval, want ≥2", heartbeats)
	}
}

// A lost lease means someone else owns the job. Continuing would post a report
// against a claim that is no longer ours.
func TestWorker_AbandonsTheRunWhenTheLeaseIsLost(t *testing.T) {
	api := newFakeAPI(t)
	api.enqueue(evalqueue.Job{ID: "job-1", SkillName: "deploy-helper", Version: 1, SuiteRef: "sha256:abc"})
	api.heartbeatErr = evalqueue.ErrLeaseLost
	release := make(chan struct{})
	runner := &fakeRunner{report: reportFixture("deploy-helper", "sha256:abc", 60), block: release}
	w, _ := worker.New(worker.Config{Endpoint: "http://x", APIKey: "k", WorkerID: "w1",
		WorkRoot: t.TempDir(), Lease: 300 * time.Millisecond, Heartbeat: 30 * time.Millisecond}, api, runner)

	done := make(chan error, 1)
	go func() { _, err := w.RunOnce(context.Background()); done <- err }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("the run was not abandoned after the lease was lost")
	}
	close(release)
	api.mu.Lock()
	posted := api.posted
	api.mu.Unlock()
	if posted != nil {
		t.Fatal("a report was posted after the lease was lost")
	}
}
