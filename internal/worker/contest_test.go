package worker_test

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/worker"
)

func contestJob(suiteRef string) evalqueue.Job {
	return evalqueue.Job{
		ID: "job-contest", ContestID: "contest-1",
		SkillID: "s1", SkillName: "alpha", Version: 2, SuiteRef: suiteRef, Tier: "smoke",
		ContestAttempts: 3,
		Candidates: []evalqueue.Candidate{
			{SkillID: "s1", SkillName: "alpha", Version: 2, Label: "alpha@2"},
			{SkillID: "s2", SkillName: "beta", Version: 5, Label: "beta@5"},
		},
	}
}

// Every candidate must run inside the one claim, against the one suite. Two
// claims mean two workers, two CLI versions and two days, which is the drift
// the contest exists to remove.
func TestRunContest_RunsEveryCandidateAgainstOneSuite(t *testing.T) {
	api := newFakeAPI(t)
	ref := fixtureSuiteRef(t)
	api.enqueue(contestJob(ref))
	r := &fakeRunner{reportSuiteRef: ref}

	w := newTestWorker(t, api, r, &fakeDeriver{})
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if api.contestReported != "contest-1" {
		t.Errorf("reported contest %q, want contest-1", api.contestReported)
	}
	if len(api.contestReports) != 2 {
		t.Fatalf("reported %d candidate reports, want 2", len(api.contestReports))
	}
	for _, label := range []string{"alpha@2", "beta@5"} {
		rep, ok := api.contestReports[label]
		if !ok {
			t.Fatalf("no report for %s", label)
		}
		if rep.SuiteRef != ref {
			t.Errorf("%s ran against suite %q, want the contest's %q", label, rep.SuiteRef, ref)
		}
	}
}

// A contest with no suite derives one from every candidate, never from the
// first: a suite derived from one candidate grades the others against that
// candidate's claims.
func TestRunContest_DerivesFromEveryCandidate(t *testing.T) {
	api := newFakeAPI(t)
	ref := fixtureSuiteRef(t)
	api.enqueue(contestJob(""))
	api.pushRef = ref
	der := &fakeDeriver{result: &worker.DeriveResult{Archive: api.suiteArchive, Tasks: 12}}
	r := &fakeRunner{reportSuiteRef: ref}

	w := newTestWorker(t, api, r, der)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	der.mu.Lock()
	defer der.mu.Unlock()
	if der.contestCandidates != 2 {
		t.Errorf("derived from %d candidates, want 2", der.contestCandidates)
	}
}

// A contest job carrying one candidate is a verdict over a single entry.
func TestRunContest_RefusesASingleCandidate(t *testing.T) {
	api := newFakeAPI(t)
	ref := fixtureSuiteRef(t)
	job := contestJob(ref)
	job.Candidates = job.Candidates[:1]
	api.enqueue(job)

	w := newTestWorker(t, api, &fakeRunner{reportSuiteRef: ref}, &fakeDeriver{})
	if _, err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce accepted a one-candidate contest")
	}
	if api.failCause == "" {
		t.Error("the job was not failed on the server")
	}
	if api.contestReported != "" {
		t.Error("a verdict was reported over a single entry")
	}
}

// Every candidate runs as a contest candidate at the contest's attempts. A
// worker that drops either runs a plain eval, with a baseline and probes the
// verdict never reads, at the tier's attempts instead of the contest's.
func TestRunContest_RunsCandidatesAtTheContestAttempts(t *testing.T) {
	api := newFakeAPI(t)
	ref := fixtureSuiteRef(t)
	api.enqueue(contestJob(ref))
	r := &fakeRunner{reportSuiteRef: ref}

	w := newTestWorker(t, api, r, &fakeDeriver{})
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if in := r.input(); !in.Contest || in.Attempts != 3 {
		t.Errorf("Contest = %v, Attempts = %d, want true and 3", in.Contest, in.Attempts)
	}
}

func reusedJob(t *testing.T, ref string, grader string) evalqueue.Job {
	t.Helper()
	rep := reportFixture("alpha", ref, 60)
	rep.GraderModel = grader
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	job := contestJob(ref)
	job.Candidates[0].ReusedReport, job.Candidates[0].ReusedFrom = raw, "contest-0"
	return job
}

// A same-day report run under the same conditions stands in for the run, and
// the worker posts only what it measured.
func TestRunContest_SkipsACandidateWithAMatchingReusedReport(t *testing.T) {
	api := newFakeAPI(t)
	ref := fixtureSuiteRef(t)
	api.enqueue(reusedJob(t, ref, ""))
	r := &fakeRunner{reportSuiteRef: ref}

	w := newTestWorker(t, api, r, &fakeDeriver{})
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(r.ran) != 1 || r.ran[0] != "beta" {
		t.Errorf("ran %v, want only beta", r.ran)
	}
	if _, ok := api.contestReports["alpha@2"]; ok || len(api.contestReports) != 1 {
		t.Errorf("posted %d reports, want only beta's", len(api.contestReports))
	}
}

// A reused report from other conditions would compare two candidates under
// two graders, which is the drift a contest exists to remove.
func TestRunContest_RunsACandidateWhoseReusedReportDiffers(t *testing.T) {
	api := newFakeAPI(t)
	ref := fixtureSuiteRef(t)
	api.enqueue(reusedJob(t, ref, "another-grader"))
	r := &fakeRunner{reportSuiteRef: ref}

	w := newTestWorker(t, api, r, &fakeDeriver{})
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	sort.Strings(r.ran)
	if len(r.ran) != 2 || r.ran[0] != "alpha" || r.ran[1] != "beta" {
		t.Errorf("ran %v, want alpha and beta", r.ran)
	}
	if len(api.contestReports) != 2 {
		t.Errorf("posted %d reports, want 2", len(api.contestReports))
	}
}
