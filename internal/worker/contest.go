package worker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/evalqueue"
)

// runContest runs every candidate against one suite, inside one claim, and
// sends the reports for the server to decide on.
//
// One claim is the whole point: the panel, the agent CLI version, the grader
// and the day are held constant by running together, rather than detected
// afterwards by report.Comparable.
func (w *Worker) runContest(ctx context.Context, job *evalqueue.Job, token string) error {
	if len(job.Candidates) < 2 {
		return fmt.Errorf("worker: contest job %s carries %d candidates", job.ID, len(job.Candidates))
	}
	if job.ContestAttempts < 1 {
		return fmt.Errorf("worker: contest job %s carries no attempts per task", job.ID)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	leaseLost := make(chan struct{})
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go func() {
		defer hbWG.Done()
		w.heartbeatLoop(ctx, runCtx, cancel, job.ID, token, leaseLost)
	}()

	bundles := make([][]byte, len(job.Candidates))
	for i, cand := range job.Candidates {
		var b []byte
		var err error
		if cand.PRHeadID != "" {
			b, err = w.api.FetchPRHead(runCtx, cand.PRHeadID)
		} else {
			b, err = w.api.FetchBundle(runCtx, cand.SkillName, cand.Version)
		}
		if err != nil {
			return fmt.Errorf("worker: fetch bundle for %s: %w", cand.Label, err)
		}
		bundles[i] = b
	}

	suiteRef, err := w.contestSuite(runCtx, job, bundles, token)
	if err != nil {
		return err
	}

	suiteArchive, err := w.api.FetchSuite(runCtx, suiteRef)
	if err != nil {
		return fmt.Errorf("worker: fetch suite: %w", err)
	}
	meta, err := w.api.SuiteMeta(runCtx, suiteRef)
	if err != nil {
		return fmt.Errorf("worker: fetch suite meta: %w", err)
	}

	tier := job.Tier
	if tier == "" {
		tier = w.cfg.Tier
	}
	run := func(idx []int) (map[string]*report.Report, error) {
		return w.runCandidates(runCtx, job, idx, bundles, suiteArchive, suiteRef, tier, meta.MachineGenerated)
	}

	var fresh, offered []int
	for i, cand := range job.Candidates {
		if len(cand.ReusedReport) > 0 {
			offered = append(offered, i)
		} else {
			fresh = append(fresh, i)
		}
	}
	if len(fresh) == 0 {
		// A reused report needs a fresh one to be checked against.
		fresh, offered = offered[:1], offered[1:]
	}

	reports, err := run(fresh)
	if err != nil {
		return err
	}
	var rerun []int
	for _, i := range offered {
		cand := job.Candidates[i]
		if why := reuseRefusal(cand, reports[job.Candidates[fresh[0]].Label]); why != "" {
			log.Info().Str("job_id", string(job.ID)).Str("contest", job.ContestID).Str("candidate", cand.Label).
				Str("reused_from", cand.ReusedFrom).Str("reason", why).
				Msg("worker: running a candidate whose same-day report ran under other conditions")
			rerun = append(rerun, i)
		}
	}
	if len(rerun) > 0 {
		more, err := run(rerun)
		if err != nil {
			return err
		}
		for label, rep := range more {
			reports[label] = rep
		}
	}

	cancel()
	hbWG.Wait()

	select {
	case <-leaseLost:
		return fmt.Errorf("worker: lease lost; contest abandoned")
	default:
	}

	// Only what this claim measured. The server keeps each reused report it
	// offered for a candidate missing here.
	if err := w.api.PostContestReport(ctx, job.ContestID, job.ID, token, reports); err != nil {
		return fmt.Errorf("worker: post contest report: %w", err)
	}
	return nil
}

// reuseRefusal says why an offered report cannot stand in for a run in this
// claim, or "" when it can.
func reuseRefusal(cand evalqueue.Candidate, fresh *report.Report) string {
	reused, err := report.Load(bytes.NewReader(cand.ReusedReport))
	if err != nil {
		return "the reused report does not load: " + err.Error()
	}
	if ok, why := reused.SameConditions(fresh); !ok {
		return why
	}
	return ""
}

// runCandidates runs the candidates at idx, at most ContestParallel at once.
// The first failure cancels the others: a verdict needs every candidate.
func (w *Worker) runCandidates(ctx context.Context, job *evalqueue.Job, idx []int, bundles [][]byte,
	suiteArchive []byte, suiteRef, tier string, allowVoid bool) (map[string]*report.Report, error) {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(w.cfg.ContestParallel)

	var mu sync.Mutex
	out := make(map[string]*report.Report, len(idx))
	for _, i := range idx {
		cand := job.Candidates[i]
		bundle := bundles[i]
		g.Go(func() error {
			rep, err := w.runCandidate(gctx, job, cand, bundle, suiteArchive, suiteRef, tier, allowVoid)
			if err != nil {
				return err
			}
			mu.Lock()
			out[cand.Label] = rep
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// contestSuite returns the suite every candidate runs against, deriving one
// from all of them when the job names none.
func (w *Worker) contestSuite(ctx context.Context, job *evalqueue.Job, bundles [][]byte, token string) (string, error) {
	if job.SuiteRef != "" {
		return job.SuiteRef, nil
	}
	if w.deriver == nil {
		return "", fmt.Errorf("worker: contest job %s names no suite and this worker has no deriver configured", job.ID)
	}

	ins := make([]DeriveInput, 0, len(job.Candidates))
	for i, cand := range job.Candidates {
		ins = append(ins, DeriveInput{
			Skill: cand.SkillName, Bundle: bundles[i], Tier: job.Tier, Panel: job.Panel, Eval: job.Eval,
		})
	}
	res, err := w.deriver.Contest(ctx, ins)
	if err != nil {
		return "", fmt.Errorf("worker: derive contest suite: %w", err)
	}
	// Pushed under the first candidate's name, which is only where the suite
	// is filed. What decides whether it can release anything is its origin,
	// and a worker's own push is always derived.
	ref, err := w.api.PushSuite(ctx, PushSuiteInput{
		Skill: job.Candidates[0].SkillName, Archive: res.Archive,
		Spec: res.Spec, JobID: job.ID, ClaimToken: token,
	})
	if err != nil {
		return "", fmt.Errorf("worker: push contest suite: %w", err)
	}
	log.Info().Str("job_id", string(job.ID)).Str("contest", job.ContestID).
		Str("suite_ref", ref).Int("tasks", res.Tasks).Int("candidates", len(job.Candidates)).
		Msg("worker: derived a contest suite from every candidate")
	return ref, nil
}

// runCandidate evaluates one candidate in its own workspace. Separate
// directories, because two candidates install a skill into the same place.
func (w *Worker) runCandidate(ctx context.Context, job *evalqueue.Job, cand evalqueue.Candidate,
	bundle, suiteArchive []byte, suiteRef, tier string, allowVoid bool) (*report.Report, error) {

	workDir, err := os.MkdirTemp(w.cfg.WorkRoot, "skael-contest-*")
	if err != nil {
		return nil, fmt.Errorf("worker: create workspace for %s: %w", cand.Label, err)
	}
	defer os.RemoveAll(workDir)

	meta, err := w.api.SuiteMeta(ctx, suiteRef)
	if err != nil {
		return nil, fmt.Errorf("worker: fetch suite meta: %w", err)
	}
	st, err := Materialize(workDir, MaterializeInput{
		Skill: cand.SkillName, Bundle: bundle, SuiteArchive: suiteArchive,
		Spec: meta.Spec, WantSuiteRef: suiteRef,
	})
	if err != nil {
		return nil, fmt.Errorf("worker: materialize workspace for %s: %w", cand.Label, err)
	}
	defer st.Close()

	rep, err := w.runner.Run(ctx, RunInput{
		JobID: job.ID,
		Skill: cand.SkillName, Version: cand.Version, SuiteRef: suiteRef,
		Tier: tier, Panel: job.Panel, Eval: job.Eval, WorkspaceDir: workDir,
		AllowVoid: allowVoid, Contest: true, Attempts: job.ContestAttempts,
	})
	if err != nil {
		return nil, fmt.Errorf("worker: run %s: %w", cand.Label, err)
	}
	if rep == nil {
		return nil, fmt.Errorf("worker: run %s returned no report and no error", cand.Label)
	}
	if rep.SuiteRef != suiteRef {
		return nil, fmt.Errorf("worker: report suite_ref %q for %s does not match the contest suite %q",
			rep.SuiteRef, cand.Label, suiteRef)
	}
	return rep, nil
}
