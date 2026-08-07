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
	"github.com/skael-dev/skael/internal/eval/spec"
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

// fixtureSuiteRef is suite.Ref of the tree fixtureSuiteArchive packs. Tests
// enqueue jobs with this as SuiteRef, so it matches what Materialize
// actually computes from the fetched archive — since Materialize fails fast
// on a mismatch (see TestMaterialize_FailsFastWhenSuiteRefDoesNotMatchWantSuiteRef),
// a job whose SuiteRef doesn't describe the real fixture content would never
// get past materialization.
func fixtureSuiteRef(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureSuite(t, dir)
	ref, err := suite.Ref(dir)
	if err != nil {
		t.Fatalf("fixtureSuiteRef: %v", err)
	}
	return ref
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
// recording of what the worker reported back.
type fakeAPI struct {
	t *testing.T

	mu           sync.Mutex
	queue        []evalqueue.Job
	reported     *report.Report
	failCause    string
	heartbeats   int
	heartbeatErr error

	bundle []byte
	// suiteArchive is what FetchSuite returns for any ref not named in
	// suiteArchives. suiteArchives lets a test give a specific ref content
	// that actually hashes to it — Materialize verifies the fetched archive
	// against the ref it asked for, so a test that wants a job to run with the
	// ref it declared must register content for it.
	suiteArchive  []byte
	suiteArchives map[string][]byte
	checks        []evalsuite.Check
	// spec is nil by default: SuiteMeta returns SuiteMeta{Spec: nil}, which
	// exercises Materialize's frontmatter-reconstruction fallback, same as
	// before this field existed.
	spec *spec.SkillSpec
	// origin is what SuiteMeta reports as the suite's origin. Zero value ("")
	// is neither OriginDerived nor OriginAuthored, so AllowVoid is false by
	// default — a test that cares must set it explicitly.
	origin evalsuite.Origin

	// job, when set, is claimed once by Claim before the queue is consulted —
	// a shorthand for tests that only care about a single job, so they don't
	// need enqueue's slice ceremony.
	job *evalqueue.Job
	// pushRef is what PushSuite returns.
	pushRef     string
	pushedSkill string
	pushedJobID string
	pushedToken string
	pushErr     error
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
	if f.job != nil {
		j := f.job
		f.job = nil
		return j, "claim-token", true, nil
	}
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
	f.reported = r
	return nil
}

func (f *fakeAPI) FailJob(_ context.Context, _ evalqueue.JobID, _, cause string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failCause = cause
	return nil
}

func (f *fakeAPI) FetchSuite(_ context.Context, ref string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.suiteArchives[ref]; ok {
		return a, nil
	}
	return f.suiteArchive, nil
}

func (f *fakeAPI) FetchBundle(_ context.Context, _ string, _ int) ([]byte, error) {
	return f.bundle, nil
}

func (f *fakeAPI) SuiteMeta(_ context.Context, _ string) (worker.SuiteMeta, error) {
	return worker.SuiteMeta{Checks: f.checks, Spec: f.spec, Origin: f.origin}, nil
}

func (f *fakeAPI) PushSuite(_ context.Context, in worker.PushSuiteInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushedSkill = in.Skill
	f.pushedJobID = string(in.JobID)
	f.pushedToken = in.ClaimToken
	return f.pushRef, f.pushErr
}

// fakeRunner is a test double for worker.Runner: no Docker, just a canned
// report/error, optionally gated behind a channel so a test can control how
// long a run stays "in flight".
type fakeRunner struct {
	report *report.Report
	err    error
	block  chan struct{}

	// reportSuiteRef is a shorthand for tests that only care about the
	// report's suite_ref: when report is nil and this is non-empty, Run
	// builds a minimal report naming it. Leaving both zero preserves the
	// original fakeRunner{} behaviour of returning (nil, nil).
	reportSuiteRef string

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
	if f.report != nil {
		return f.report, nil
	}
	if f.reportSuiteRef != "" {
		return reportFixture(in.Skill, f.reportSuiteRef, 60), nil
	}
	return nil, nil
}

func (f *fakeRunner) input() worker.RunInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotInput
}

// fakeDeriver is a test double for worker.Deriver: no LLM, no Docker, just a
// canned suite (or error) and a call count.
type fakeDeriver struct {
	result *worker.DeriveResult
	err    error

	mu    sync.Mutex
	calls int
}

func (f *fakeDeriver) Derive(_ context.Context, _ worker.DeriveInput) (*worker.DeriveResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()

	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &worker.DeriveResult{
		Archive: []byte("fake-derived-archive"),
		Checks:  []evalsuite.Check{{TaskID: "t1", OK: true}},
		Spec:    &spec.SkillSpec{Name: "demo"},
	}, nil
}

// newTestWorker builds a Worker over api with a short-lived WorkRoot, filling
// in whatever fixture data api didn't set explicitly so a test can construct
// a bare fakeAPI naming only the fields its scenario cares about. der may be
// nil — that is the scenario TestRunOnce_FailsCleanlyWithNoDeriver exercises.
func newTestWorker(t *testing.T, api *fakeAPI, r *fakeRunner, der worker.Deriver) *worker.Worker {
	t.Helper()
	if api.bundle == nil {
		api.bundle = fixtureBundle(t)
	}
	if api.suiteArchive == nil {
		api.suiteArchive = fixtureSuiteArchive(t)
	}
	if api.checks == nil {
		api.checks = []evalsuite.Check{{TaskID: "t1", OK: true}}
	}
	w, err := worker.New(worker.Config{Endpoint: "http://x", APIKey: "k", WorkerID: "w1", WorkRoot: t.TempDir()}, api, r, der)
	if err != nil {
		t.Fatalf("worker.New: %v", err)
	}
	return w
}

func TestWorker_RunOnce_ClaimsRunsAndPostsAReport(t *testing.T) {
	api := newFakeAPI(t)
	ref := fixtureSuiteRef(t)
	api.enqueue(evalqueue.Job{ID: "job-1", SkillName: "deploy-helper", Version: 2, SuiteRef: ref, Tier: "smoke"})
	runner := &fakeRunner{report: reportFixture("deploy-helper", ref, 71)}

	w, err := worker.New(worker.Config{Endpoint: "http://x", APIKey: "k", WorkerID: "w1", WorkRoot: t.TempDir()}, api, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := w.RunOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("RunOnce = (%v, %v)", worked, err)
	}
	if api.reported == nil {
		t.Fatal("no report was reported")
	}
	if api.reported.Headline != 71 {
		t.Fatalf("reported headline = %v", api.reported.Headline)
	}
	if runner.input().WorkspaceDir == "" {
		t.Fatal("the runner was handed no workspace")
	}
}

// A worker that dies quietly leaves the job leased until the lease lapses. A
// worker that fails loudly returns it now, with the reason recorded.
func TestWorker_RunOnce_ReportsAFailureInsteadOfSwallowingIt(t *testing.T) {
	api := newFakeAPI(t)
	api.enqueue(evalqueue.Job{ID: "job-1", SkillName: "deploy-helper", Version: 2, SuiteRef: fixtureSuiteRef(t)})
	runner := &fakeRunner{err: errors.New("sandbox unavailable")}
	w, _ := worker.New(worker.Config{Endpoint: "http://x", APIKey: "k", WorkerID: "w1", WorkRoot: t.TempDir()}, api, runner, nil)

	if _, err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce hid the run failure")
	}
	if api.failCause == "" || !strings.Contains(api.failCause, "sandbox unavailable") {
		t.Fatalf("failure cause reported = %q", api.failCause)
	}
	if api.reported != nil {
		t.Fatal("a report was reported for a failed run")
	}
}

func TestWorker_RunOnce_EmptyQueueIsNotAnError(t *testing.T) {
	w, _ := worker.New(worker.Config{Endpoint: "http://x", APIKey: "k", WorkerID: "w1", WorkRoot: t.TempDir()},
		newFakeAPI(t), &fakeRunner{}, nil)
	worked, err := w.RunOnce(context.Background())
	if err != nil || worked {
		t.Fatalf("RunOnce on an empty queue = (%v, %v), want (false, nil)", worked, err)
	}
}

func TestWorker_HeartbeatsWhileTheRunIsInFlight(t *testing.T) {
	api := newFakeAPI(t)
	ref := fixtureSuiteRef(t)
	api.enqueue(evalqueue.Job{ID: "job-1", SkillName: "deploy-helper", Version: 1, SuiteRef: ref})
	release := make(chan struct{})
	runner := &fakeRunner{report: reportFixture("deploy-helper", ref, 60), block: release}
	w, _ := worker.New(worker.Config{Endpoint: "http://x", APIKey: "k", WorkerID: "w1",
		WorkRoot: t.TempDir(), Lease: 300 * time.Millisecond, Heartbeat: 50 * time.Millisecond}, api, runner, nil)

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
	ref := fixtureSuiteRef(t)
	api.enqueue(evalqueue.Job{ID: "job-1", SkillName: "deploy-helper", Version: 1, SuiteRef: ref})
	api.heartbeatErr = evalqueue.ErrLeaseLost
	release := make(chan struct{})
	runner := &fakeRunner{report: reportFixture("deploy-helper", ref, 60), block: release}
	w, _ := worker.New(worker.Config{Endpoint: "http://x", APIKey: "k", WorkerID: "w1",
		WorkRoot: t.TempDir(), Lease: 300 * time.Millisecond, Heartbeat: 30 * time.Millisecond}, api, runner, nil)

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
	reported := api.reported
	api.mu.Unlock()
	if reported != nil {
		t.Fatal("a report was reported after the lease was lost")
	}
}

// A Runner's report must describe the job it was run for. Posting a mismatch
// would score the job under a different suite ref than the one it names —
// the server checks this too, but a worker that catches it locally can fail
// with a clear cause instead of a rejected POST.
func TestWorker_RunOnce_RefusesAReportWithMismatchedSuiteRef(t *testing.T) {
	api := newFakeAPI(t)
	api.enqueue(evalqueue.Job{ID: "job-1", SkillName: "deploy-helper", Version: 1, SuiteRef: fixtureSuiteRef(t)})
	// The report names a suite ref the job never asked for.
	runner := &fakeRunner{report: reportFixture("deploy-helper", "sha256:different", 60)}
	w, _ := worker.New(worker.Config{Endpoint: "http://x", APIKey: "k", WorkerID: "w1", WorkRoot: t.TempDir()}, api, runner, nil)

	if _, err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce accepted a report whose suite_ref did not match the job")
	}
	api.mu.Lock()
	reported := api.reported
	api.mu.Unlock()
	if reported != nil {
		t.Fatal("a report was reported despite a suite_ref mismatch")
	}
}

// A Runner returning (nil, nil) — the zero value of fakeRunner itself — must
// not panic the loop; it must be reported as a failure like any other.
func TestWorker_RunOnce_ANilReportWithNoErrorIsAFailureNotAPanic(t *testing.T) {
	api := newFakeAPI(t)
	api.enqueue(evalqueue.Job{ID: "job-1", SkillName: "deploy-helper", Version: 1, SuiteRef: fixtureSuiteRef(t)})
	runner := &fakeRunner{} // report == nil, err == nil
	w, _ := worker.New(worker.Config{Endpoint: "http://x", APIKey: "k", WorkerID: "w1", WorkRoot: t.TempDir()}, api, runner, nil)

	worked, err := w.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce accepted a nil report as success")
	}
	if !worked {
		t.Fatalf("RunOnce = (%v, %v), want worked=true (a job was claimed and attempted)", worked, err)
	}
}

// New must fail fast on a WorkRoot that cannot be created, rather than every
// subsequent RunOnce failing the same way one job at a time.
func TestNew_FailsFastOnAnUnusableWorkRoot(t *testing.T) {
	// A file, not a directory: MkdirAll under it must fail.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	badRoot := filepath.Join(blocker, "workroot")

	_, err := worker.New(worker.Config{Endpoint: "http://x", APIKey: "k", WorkerID: "w1", WorkRoot: badRoot}, newFakeAPI(t), &fakeRunner{}, nil)
	if err == nil {
		t.Fatal("New accepted a WorkRoot that cannot be created")
	}
}

func TestRunOnce_DerivesWhenTheJobHasNoSuiteRef(t *testing.T) {
	// pushRef must be a real suite.Ref of the fixture tree: fakeAPI.FetchSuite
	// always returns the fixture archive regardless of which ref is asked
	// for, and Materialize's WantSuiteRef check now runs against suiteRef
	// (what PushSuite returned) on the derive path too, so a placeholder
	// like "derived-ref" would fail materialization before this test ever
	// reaches its own assertions.
	derivedRef := fixtureSuiteRef(t)
	api := &fakeAPI{
		job:     &evalqueue.Job{ID: "j1", SkillName: "demo", Version: 1, SuiteRef: "", Tier: "full"},
		pushRef: derivedRef, // what PushSuite returns
	}
	der := &fakeDeriver{}
	w := newTestWorker(t, api, &fakeRunner{reportSuiteRef: derivedRef}, der)

	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if der.calls != 1 {
		t.Fatalf("Derive called %d times, want 1", der.calls)
	}
	if api.pushedSkill != "demo" {
		t.Fatalf("pushed suite for %q, want demo", api.pushedSkill)
	}
	if api.reported == nil || api.reported.SuiteRef != derivedRef {
		t.Fatalf("report suite_ref = %q, want the derived ref", api.reported.SuiteRef)
	}
}

// A derived suite is gated by its own oracles with nobody to repair a task
// that fails, which is why it asks for 18 rather than 10. That surplus only
// helps if the run then excludes the void tasks instead of refusing outright.
//
// AllowVoid is sourced from the suite's recorded origin (SuiteMeta.Origin),
// not from whether this particular run was the one that derived it — see
// TestRunOnce_AllowsVoidTasksOnAnyRunAgainstAnAlreadyDerivedSuite for the
// retry/follow-up case that distinction exists to fix.
func TestRunOnce_AllowsVoidTasksOnlyOnTheDerivePath(t *testing.T) {
	derivedRef := fixtureSuiteRef(t)
	api := &fakeAPI{
		job:     &evalqueue.Job{ID: "j1", SkillName: "demo", Version: 1, SuiteRef: "", Tier: "full"},
		pushRef: derivedRef,
		origin:  evalsuite.OriginDerived,
	}
	r := &fakeRunner{reportSuiteRef: derivedRef}
	if _, err := newTestWorker(t, api, r, &fakeDeriver{}).RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !r.input().AllowVoid {
		t.Fatal("a derived suite's run was not allowed to exclude void tasks")
	}

	// An authored suite has an author who can go fix a void task, so the
	// stricter contract stays regardless of which run is asking.
	authoredRef := fixtureSuiteRef(t)
	api2 := &fakeAPI{
		job:           &evalqueue.Job{ID: "j2", SkillName: "demo", Version: 1, SuiteRef: authoredRef, Tier: "full"},
		suiteArchives: map[string][]byte{authoredRef: fixtureSuiteArchive(t)},
		origin:        evalsuite.OriginAuthored,
	}
	r2 := &fakeRunner{reportSuiteRef: authoredRef}
	if _, err := newTestWorker(t, api2, r2, &fakeDeriver{}).RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if r2.input().AllowVoid {
		t.Fatal("an authored suite's run was allowed to exclude void tasks")
	}
}

// TestRunOnce_AllowsVoidTasksOnAnyRunAgainstAnAlreadyDerivedSuite proves the
// retry/follow-up fix: RecordDerivedSuite now stamps eval_jobs.suite_ref at
// push time, before the panel runs, so a retry after a mid-run failure (or
// any later eval against a previously-derived suite) sees a non-empty
// job.SuiteRef from the start — the old `derivedHere := suiteRef == ""`
// computation would then set AllowVoid: false and refuse deterministically
// on voids that were fine to accept the first time. AllowVoid must instead
// come from the suite's own recorded origin, which SuiteMeta reports on
// every run, not just the one that derived it.
func TestRunOnce_AllowsVoidTasksOnAnyRunAgainstAnAlreadyDerivedSuite(t *testing.T) {
	derivedRef := fixtureSuiteRef(t)
	api := &fakeAPI{
		// SuiteRef is already populated, as it would be on a retry or a
		// fresh follow-up eval — this is the case that was broken.
		job:           &evalqueue.Job{ID: "j3", SkillName: "demo", Version: 1, SuiteRef: derivedRef, Tier: "full"},
		suiteArchives: map[string][]byte{derivedRef: fixtureSuiteArchive(t)},
		origin:        evalsuite.OriginDerived,
	}
	r := &fakeRunner{reportSuiteRef: derivedRef}
	if _, err := newTestWorker(t, api, r, &fakeDeriver{}).RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !r.input().AllowVoid {
		t.Fatal("a run against an already-derived suite (non-empty job.SuiteRef) was not allowed to exclude void tasks")
	}
}

// The push carries the claim so the server can record the suite as derived
// there and then; without it a run that never reports leaves a
// machine-generated suite recorded as authored.
func TestRunOnce_PushesTheDerivedSuiteWithTheJobClaim(t *testing.T) {
	derivedRef := fixtureSuiteRef(t)
	api := &fakeAPI{
		job:     &evalqueue.Job{ID: "j1", SkillName: "demo", Version: 1, SuiteRef: "", Tier: "full"},
		pushRef: derivedRef,
	}
	w := newTestWorker(t, api, &fakeRunner{reportSuiteRef: derivedRef}, &fakeDeriver{})
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if api.pushedJobID != "j1" {
		t.Fatalf("pushed with job id %q, want j1", api.pushedJobID)
	}
	if api.pushedToken != "claim-token" {
		t.Fatalf("pushed with claim token %q, want the token Claim returned", api.pushedToken)
	}
}

func TestRunOnce_DoesNotDeriveWhenTheJobNamesASuite(t *testing.T) {
	// Deriving a second suite would make two scores for the same skill
	// incomparable, and cost a full derivation to do it.
	// The ref is the fixture archive's own content hash, spelled out here
	// rather than substituted by newTestWorker behind the test's back:
	// Materialize verifies the fetched archive against the ref it asked for,
	// so a job can only name a ref whose content the fake actually serves.
	existing := fixtureSuiteRef(t)
	api := &fakeAPI{
		job: &evalqueue.Job{
			ID: "j1", SkillName: "demo", Version: 1, SuiteRef: existing, Tier: "full",
		},
		suiteArchives: map[string][]byte{existing: fixtureSuiteArchive(t)},
	}
	der := &fakeDeriver{}
	w := newTestWorker(t, api, &fakeRunner{reportSuiteRef: existing}, der)

	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if der.calls != 0 {
		t.Fatalf("Derive called %d times for a job that already names a suite", der.calls)
	}
}

func TestRunOnce_FailsCleanlyWithNoDeriver(t *testing.T) {
	api := &fakeAPI{job: &evalqueue.Job{ID: "j1", SkillName: "demo", Version: 1, SuiteRef: ""}}
	w := newTestWorker(t, api, &fakeRunner{}, nil)

	if _, err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce accepted a derive job with no Deriver")
	}
	if api.failCause == "" || !strings.Contains(api.failCause, "derive") {
		t.Fatalf("job failed with cause %q, which does not name the missing deriver", api.failCause)
	}
}

func TestRunOnce_ReportMustMatchTheDerivedRef(t *testing.T) {
	// The invariant still holds; it is just checked against the ref the worker
	// derived rather than the empty one it claimed. pushRef must still be a
	// real suite.Ref (see TestRunOnce_DerivesWhenTheJobHasNoSuiteRef) so this
	// fails on the intended report/derived-ref mismatch, not on Materialize's
	// unrelated WantSuiteRef check rejecting a placeholder first.
	api := &fakeAPI{
		job:     &evalqueue.Job{ID: "j1", SkillName: "demo", Version: 1, SuiteRef: ""},
		pushRef: fixtureSuiteRef(t),
	}
	w := newTestWorker(t, api, &fakeRunner{reportSuiteRef: "something-else"}, &fakeDeriver{})

	if _, err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce accepted a report naming a different suite")
	}
}
