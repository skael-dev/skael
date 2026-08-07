package evalqueue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/quality"
	"github.com/skael-dev/skael/internal/skill"
)

type claimBody struct {
	WorkerID     string `json:"worker_id" minLength:"1"`
	LeaseSeconds int    `json:"lease_seconds,omitempty"`
}

type claimInput struct {
	Body claimBody
}

// jobOutput is the wire shape for a Job, exposed to workers and status
// pollers.
type jobOutput struct {
	ID        string `json:"id"`
	SkillID   string `json:"skill_id"`
	SkillName string `json:"skill_name"`
	Version   int    `json:"version"`
	SuiteRef  string `json:"suite_ref"`
	Tier      string `json:"tier"`
	// Agents and Models carry the requested panel (see Panel). Without them
	// a worker claiming this job over HTTP has no way to learn which panel
	// was asked for and silently falls back to its own default — defeating
	// the point of the re-run endpoint's agents/models parameters.
	Agents      []string   `json:"agents,omitempty"`
	Models      []string   `json:"models,omitempty"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	MaxAttempts int        `json:"max_attempts"`
	WorkerID    string     `json:"worker_id,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	RequestedBy string     `json:"requested_by,omitempty"`
	EnqueuedAt  time.Time  `json:"enqueued_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	// QueuePosition is how many queued jobs are ahead of this one, and 0
	// whenever the job is not queued. It is a hint: retries and concurrency
	// move it, so the UI shows a position and never an ETA.
	QueuePosition int `json:"queue_position"`
}

func toJobOutput(j *Job) jobOutput {
	return jobOutput{
		ID:          string(j.ID),
		SkillID:     j.SkillID,
		SkillName:   j.SkillName,
		Version:     j.Version,
		SuiteRef:    j.SuiteRef,
		Tier:        j.Tier,
		Agents:      j.Panel.Agents,
		Models:      j.Panel.Models,
		Status:      string(j.Status),
		Attempts:    j.Attempts,
		MaxAttempts: j.MaxAttempts,
		WorkerID:    j.WorkerID,
		LastError:   j.LastError,
		RequestedBy: j.RequestedBy,
		EnqueuedAt:  j.CreatedAt,
		StartedAt:   j.StartedAt,
	}
}

type claimOutputBody struct {
	Job        jobOutput `json:"job"`
	ClaimToken string    `json:"claim_token"`
}

type claimOutput struct {
	Status int
	Body   claimOutputBody
}

type emptyOutput struct {
	Status int
}

type heartbeatInput struct {
	ID         string `path:"id"`
	ClaimToken string `header:"X-Claim-Token"`
}

type failBody struct {
	Error string `json:"error,omitempty"`
}

type failInput struct {
	ID         string `path:"id"`
	ClaimToken string `header:"X-Claim-Token"`
	Body       failBody
}

type reportInput struct {
	ID         string `path:"id"`
	ClaimToken string `header:"X-Claim-Token"`
	RawBody    []byte
}

type getJobInput struct {
	ID string `path:"id"`
}

type getJobOutput struct {
	Body jobOutput
}

type cancelJobInput struct {
	ID string `path:"id"`
}

// rerunBody is the request to re-run an evaluation for a skill. Every field
// is optional: omitting version re-runs the latest, omitting suite_ref uses
// the skill's stored suite (never a freshly generated one — that would be a
// different measurement), omitting tier defaults to "full", and omitting
// agents/models keeps the worker's default panel.
type rerunBody struct {
	Version  int      `json:"version,omitempty"`
	Tier     string   `json:"tier,omitempty"`
	Agents   []string `json:"agents,omitempty"`
	Models   []string `json:"models,omitempty"`
	SuiteRef string   `json:"suite_ref,omitempty"`
}

type rerunInput struct {
	Name string `path:"name"`
	Body rerunBody
}

type rerunOutputBody struct {
	JobID string `json:"job_id"`
}

type rerunOutput struct {
	Status int
	Body   rerunOutputBody
}

// defaultLeaseSeconds is used when a claim request omits lease_seconds.
const defaultLeaseSeconds = 60

// RouteOptions carries the collaborators the report handler needs to act on a
// score beyond storing it. Each is optional: an operator running without the
// gate wired must not hit a nil dereference.
type RouteOptions struct {
	// Releaser re-decides a held version once its score lands. Nil disables
	// the re-decision entirely: reports are still ingested, held versions
	// simply stay held.
	Releaser *skill.Releaser
	// QualityFloor is the minimum headline score a verified report must
	// reach to clear a held version. It comes from
	// platform.Config.QualityFloor, the same value publish decides with.
	QualityFloor float64
}

// RegisterRoutes wires up the eval job queue HTTP endpoints: claim,
// heartbeat, report ingestion, fail, status lookup, and re-run.
func RegisterRoutes(api huma.API, q *PoolExecutor, qual *quality.Store, skills *skill.Store, suites *evalsuite.Registry, opts RouteOptions) {
	huma.Register(api, huma.Operation{
		OperationID:   "claim-eval-job",
		Method:        http.MethodPost,
		Path:          "/api/eval/jobs/claim",
		Summary:       "Claim the next eval job",
		DefaultStatus: http.StatusOK,
		Responses: map[string]*huma.Response{
			"204": {Description: "no job is currently claimable"},
		},
	}, func(ctx context.Context, input *claimInput) (*claimOutput, error) {
		u := auth.UserFromContext(ctx)
		if !u.IsPrivileged() {
			return nil, huma.Error403Forbidden("claim eval job: privileged callers only")
		}

		lease := input.Body.LeaseSeconds
		if lease <= 0 {
			lease = defaultLeaseSeconds
		}

		j, token, ok, err := q.Claim(ctx, input.Body.WorkerID, time.Duration(lease)*time.Second)
		if err != nil {
			log.Error().Err(err).Msg("evalqueue: claim failed")
			return nil, huma.Error500InternalServerError("claim eval job: internal error")
		}
		if !ok {
			return &claimOutput{Status: http.StatusNoContent}, nil
		}

		return &claimOutput{
			Status: http.StatusOK,
			Body: claimOutputBody{
				Job:        toJobOutput(j),
				ClaimToken: token,
			},
		}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "heartbeat-eval-job",
		Method:        http.MethodPost,
		Path:          "/api/eval/jobs/{id}/heartbeat",
		Summary:       "Extend an eval job's lease",
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, input *heartbeatInput) (*emptyOutput, error) {
		jobID := JobID(input.ID)
		j, ok, err := q.VerifyClaim(ctx, jobID, input.ClaimToken)
		if err != nil {
			log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: verify claim failed")
			return nil, huma.Error500InternalServerError("heartbeat eval job: internal error")
		}
		if !ok {
			// VerifyClaim cannot distinguish a forged token from a token
			// that was legitimately right, on a job that has since moved on
			// — Cancel clears claim_token_hash along with the status, so a
			// cancelled job never re-verifies regardless of the token. Look
			// the job up directly: if it exists and is either no longer
			// running, or still "running" but its lease has already lapsed
			// (VerifyClaim requires lease_expires_at > now(), so a lapsed
			// lease fails verification even though the row's status column
			// hasn't caught up yet) — either way that is the lease-lost case
			// the worker needs to hear about as 409, distinct from an
			// outright forged/unknown token (403).
			job, getErr := q.Get(ctx, jobID)
			if getErr != nil {
				log.Error().Err(getErr).Str("job_id", input.ID).Msg("evalqueue: get job failed")
				return nil, huma.Error500InternalServerError("heartbeat eval job: internal error")
			}
			if job != nil {
				leaseLapsed := job.LeaseExpiresAt != nil && job.LeaseExpiresAt.Before(time.Now())
				if job.Status != StatusRunning || leaseLapsed {
					return nil, huma.Error409Conflict("heartbeat eval job: lease lost")
				}
			}
			return nil, huma.Error403Forbidden("heartbeat eval job: invalid claim")
		}

		if err := q.Heartbeat(ctx, j.ID, j.WorkerID); err != nil {
			if errors.Is(err, ErrLeaseLost) {
				return nil, huma.Error409Conflict("heartbeat eval job: lease lost")
			}
			log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: heartbeat failed")
			return nil, huma.Error500InternalServerError("heartbeat eval job: internal error")
		}
		return &emptyOutput{Status: http.StatusOK}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "fail-eval-job",
		Method:        http.MethodPost,
		Path:          "/api/eval/jobs/{id}/fail",
		Summary:       "Report an eval job as failed",
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, input *failInput) (*emptyOutput, error) {
		j, ok, err := q.VerifyClaim(ctx, JobID(input.ID), input.ClaimToken)
		if err != nil {
			log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: verify claim failed")
			return nil, huma.Error500InternalServerError("fail eval job: internal error")
		}
		if !ok {
			return nil, huma.Error403Forbidden("fail eval job: invalid claim")
		}

		if err := q.Fail(ctx, j.ID, j.WorkerID, input.Body.Error); err != nil {
			if errors.Is(err, ErrLeaseLost) {
				return nil, huma.Error409Conflict("fail eval job: lease lost")
			}
			log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: fail failed")
			return nil, huma.Error500InternalServerError("fail eval job: internal error")
		}
		return &emptyOutput{Status: http.StatusOK}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-eval-job",
		Method:      http.MethodGet,
		Path:        "/api/eval/jobs/{id}",
		Summary:     "Get an eval job's status",
	}, func(ctx context.Context, input *getJobInput) (*getJobOutput, error) {
		j, err := q.Get(ctx, JobID(input.ID))
		if err != nil {
			log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: get job failed")
			return nil, huma.Error500InternalServerError("get eval job: internal error")
		}
		if j == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("job %q not found", input.ID))
		}
		out := toJobOutput(j)
		if j.Status == StatusQueued {
			pos, err := q.QueuePosition(ctx, j.ID)
			if err != nil {
				log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: queue position failed")
				return nil, huma.Error500InternalServerError("get eval job: internal error")
			}
			out.QueuePosition = pos
		}
		return &getJobOutput{Body: out}, nil
	})

	type skillEvalsInput struct {
		Name string `path:"name"`
	}
	type skillEvalsBody struct {
		Jobs []jobOutput `json:"jobs"`
	}
	type skillEvalsOutput struct {
		Body skillEvalsBody
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-skill-evals",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}/evals",
		Summary:     "List eval jobs for a skill, newest first",
	}, func(ctx context.Context, input *skillEvalsInput) (*skillEvalsOutput, error) {
		sk, err := skills.GetByName(ctx, input.Name)
		if err != nil {
			log.Error().Err(err).Str("skill", input.Name).Msg("evalqueue: get skill failed")
			return nil, huma.Error500InternalServerError("list skill evals: internal error")
		}
		if sk == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("skill %q not found", input.Name))
		}
		jobs, err := q.ListBySkill(ctx, sk.ID)
		if err != nil {
			log.Error().Err(err).Str("skill", input.Name).Msg("evalqueue: list skill evals failed")
			return nil, huma.Error500InternalServerError("list skill evals: internal error")
		}
		out := skillEvalsBody{Jobs: make([]jobOutput, 0, len(jobs))}
		for i := range jobs {
			j := &jobs[i]
			o := toJobOutput(j)
			if j.Status == StatusQueued {
				pos, err := q.QueuePosition(ctx, j.ID)
				if err != nil {
					log.Error().Err(err).Str("skill", input.Name).Msg("evalqueue: queue position failed")
					return nil, huma.Error500InternalServerError("list skill evals: internal error")
				}
				o.QueuePosition = pos
			}
			out.Jobs = append(out.Jobs, o)
		}
		return &skillEvalsOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "cancel-eval-job",
		Method:        http.MethodPost,
		Path:          "/api/eval/jobs/{id}/cancel",
		Summary:       "Cancel an eval job that has not finished",
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, input *cancelJobInput) (*emptyOutput, error) {
		u := auth.UserFromContext(ctx)
		if !u.IsPrivileged() {
			return nil, huma.Error403Forbidden("cancel eval job: privileged callers only")
		}

		if err := q.Cancel(ctx, JobID(input.ID)); err != nil {
			if errors.Is(err, ErrNotCancellable) {
				return nil, huma.Error409Conflict("cancel eval job: job already finished")
			}
			log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: cancel failed")
			return nil, huma.Error500InternalServerError("cancel eval job: internal error")
		}
		return &emptyOutput{Status: http.StatusOK}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "report-eval-job",
		Method:        http.MethodPost,
		Path:          "/api/eval/jobs/{id}/report",
		Summary:       "Ingest an eval report and complete the job",
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, input *reportInput) (*emptyOutput, error) {
		jobID := JobID(input.ID)

		// a. Verify the claim before touching anything else. A rejected
		// claim never reveals whether the report itself would have parsed.
		j, ok, err := q.VerifyClaim(ctx, jobID, input.ClaimToken)
		if err != nil {
			log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: verify claim failed")
			return nil, huma.Error500InternalServerError("report eval job: internal error")
		}
		if !ok {
			return nil, huma.Error403Forbidden("report eval job: invalid claim")
		}

		// b. Parse the report. A malformed or newer-schema report is a 400,
		// not an infra failure.
		rep, err := report.Load(bytes.NewReader(input.RawBody))
		if err != nil {
			return nil, huma.Error400BadRequest("report eval job: " + err.Error())
		}

		// c. The report must describe the job it claims to answer. Enforced
		// before constructing the quality record — the report's Skill/
		// SuiteRef are validated against the job's own identity, never used
		// as the destination.
		if rep.Skill != j.SkillName {
			return nil, huma.Error422UnprocessableEntity(fmt.Sprintf(
				"report eval job: report skill %q does not match job skill %q", rep.Skill, j.SkillName))
		}
		// A job submitted with no suite_ref had its suite derived by the
		// worker, so the report names a ref the job row could not. It is
		// validated here instead: refs are globally unique across skills
		// and eval_jobs.suite_ref carries no foreign key, so an unchecked
		// ref would attribute a score computed against another skill's
		// tasks to this one.
		derived := false
		switch {
		case j.SuiteRef == "":
			sr, err := suites.Get(ctx, rep.SuiteRef)
			if errors.Is(err, evalsuite.ErrNotFound) {
				return nil, huma.Error422UnprocessableEntity(fmt.Sprintf(
					"report eval job: report suite_ref %q is not a registered suite", rep.SuiteRef))
			}
			if err != nil {
				log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: suite lookup failed")
				return nil, huma.Error500InternalServerError("report eval job: internal error")
			}
			if sr.SkillName != j.SkillName {
				return nil, huma.Error422UnprocessableEntity(fmt.Sprintf(
					"report eval job: suite %q belongs to skill %q, not %q", rep.SuiteRef, sr.SkillName, j.SkillName))
			}
			derived = true
		case rep.SuiteRef != j.SuiteRef:
			return nil, huma.Error422UnprocessableEntity(fmt.Sprintf(
				"report eval job: report suite_ref %q does not match job suite_ref %q", rep.SuiteRef, j.SuiteRef))
		default:
			// A re-run against a suite derived earlier still measures the
			// skill against its own claims. Checking only the empty-ref
			// case would call every run after the first one authored.
			sr, err := suites.Get(ctx, rep.SuiteRef)
			if errors.Is(err, evalsuite.ErrNotFound) {
				return nil, huma.Error422UnprocessableEntity(fmt.Sprintf(
					"report eval job: report suite_ref %q is not a registered suite", rep.SuiteRef))
			}
			if err != nil {
				log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: suite lookup failed")
				return nil, huma.Error500InternalServerError("report eval job: internal error")
			}
			derived = sr.Origin == evalsuite.OriginDerived
		}
		// An empty or "dev" engine_version defeats report.Comparable's check
		// for whether two scores come from the same worker build — a
		// constant value means that check can never fire, and scores from
		// different worker builds get charted as one trend. "dev" is the
		// unset default a binary built without -ldflags -X main.version=...
		// carries; reject it here rather than storing it.
		if rep.EngineVersion == "" || rep.EngineVersion == "dev" {
			return nil, huma.Error422UnprocessableEntity(fmt.Sprintf(
				"report eval job: engine_version %q is not acceptable; the worker binary must be built with its version set", rep.EngineVersion))
		}

		// d. FromReport is pure validation/mapping; a malformed report at
		// this stage is a 400, an unexpected error is a 500.
		rec, err := quality.FromReportRaw(rep, input.RawBody)
		if err != nil {
			return nil, huma.Error400BadRequest("report eval job: " + err.Error())
		}

		// e. Identity and version come from the job row, never the report
		// body — a worker cannot nominate which skill or version its number
		// lands on. ScoredAt is likewise set here, not taken from the
		// report's finished_at: that field is worker-supplied and
		// unvalidated, and it is the sole ordering key for
		// Latest/LatestAcrossVersions/History. A worker that omits it (the
		// zero value, 0001-01-01) would sort last forever; a worker with a
		// fast clock would permanently pin its score as "latest".
		rec.Verified = true
		rec.SkillID = j.SkillID
		rec.Version = j.Version
		rec.JobID = string(j.ID)
		rec.ScoredAt = time.Now()
		rec.SuiteDerived = derived

		// f/g. Persist the quality record and mark the job done in a single
		// transaction. Without this, a transient failure between the two
		// writes (Upsert succeeds, Complete fails) leaves the claim valid
		// and the job still "running" — a well-behaved worker's retry would
		// then Upsert a second verified row for the same job. A shared
		// transaction makes the pair atomic: either both land, or neither
		// does and the worker's retry is a clean first attempt. This was
		// chosen over a conflict-aware (ON CONFLICT) write because Upsert is
		// deliberately insert-only — it keeps score history across re-scores
		// — so there is no natural per-job uniqueness to key a conflict
		// clause on without changing that contract.
		tx, err := q.Pool().Begin(ctx)
		if err != nil {
			log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: begin transaction failed")
			return nil, huma.Error500InternalServerError("report eval job: internal error")
		}
		defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

		if j.SuiteRef == "" {
			if err := suites.MarkDerived(ctx, tx, rep.SuiteRef); err != nil {
				log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: mark suite derived failed")
				return nil, huma.Error500InternalServerError("report eval job: internal error")
			}
			if err := q.WithExecutor(tx).SetSuiteRef(ctx, j.ID, rep.SuiteRef); err != nil {
				log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: record derived suite ref failed")
				return nil, huma.Error500InternalServerError("report eval job: internal error")
			}
		}

		if err := qual.WithExecutor(tx).Upsert(ctx, rec); err != nil {
			log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: upsert quality record failed")
			return nil, huma.Error500InternalServerError("report eval job: internal error")
		}
		if err := q.WithExecutor(tx).Complete(ctx, j.ID, j.WorkerID); err != nil {
			log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: complete job failed")
			return nil, huma.Error500InternalServerError("report eval job: internal error")
		}
		// h. Re-decide the version now that a measurement exists: a version
		// held on an appealable finding is released if this score clears it.
		// Without this the gate never lets go by itself.
		//
		// Note the deliberate asymmetry with the transaction. The release
		// writes through tx, so it cannot outlive a rolled-back score. But a
		// release *error* does not roll the score back: a verified
		// measurement is worth keeping whatever the gate write did, and the
		// version simply stays held, which is the safe direction.
		//
		// The release runs inside a savepoint (pgx models a nested Begin as
		// one) rather than directly on tx, because that asymmetry is
		// otherwise unachievable: a failed statement aborts the whole
		// Postgres transaction, so a release error would take the score down
		// with it at Commit. The savepoint confines the damage to the
		// release.
		if opts.Releaser != nil {
			sp, spErr := tx.Begin(ctx)
			if spErr != nil {
				log.Error().Err(spErr).Str("job_id", input.ID).Msg("evalqueue: begin release savepoint failed")
			} else if _, released, err := opts.Releaser.Reconsider(ctx, sp, j.SkillName, j.Version,
				skill.QualityEvidence{
					Verified:                 rec.Verified,
					PanelComplete:            rec.PanelComplete,
					Headline:                 rec.Headline,
					CriticalForbidViolations: rec.CriticalForbidViolations,
					SuiteDerived:             rec.SuiteDerived,
				}, opts.QualityFloor); err != nil {
				_ = sp.Rollback(ctx)
				log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: reconsider held version failed")
			} else {
				if err := sp.Commit(ctx); err != nil {
					log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: release savepoint commit failed")
				} else if released {
					log.Info().Str("skill", j.SkillName).Int("version", j.Version).
						Msg("evalqueue: report released a held version")
				}
			}
		}

		if err := tx.Commit(ctx); err != nil {
			log.Error().Err(err).Str("job_id", input.ID).Msg("evalqueue: commit transaction failed")
			return nil, huma.Error500InternalServerError("report eval job: internal error")
		}

		return &emptyOutput{Status: http.StatusOK}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "rerun-eval",
		Method:        http.MethodPost,
		Path:          "/api/skills/{name}/evals",
		Summary:       "Re-run an evaluation against a different model panel",
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *rerunInput) (*rerunOutput, error) {
		u := auth.UserFromContext(ctx)
		if !u.IsPrivileged() {
			return nil, huma.Error403Forbidden("rerun eval: privileged callers only")
		}

		// Validated here rather than with a Huma enum tag: an enum tag makes an
		// omitted value invalid, which would break the "" -> full default
		// below. Left unchecked, an unknown tier is stored on the job and only
		// fails inside the worker at BuildPlan — after a claim, a lease and a
		// materialised workspace have been spent on a job that could never run.
		switch input.Body.Tier {
		case "", string(runner.TierSmoke), string(runner.TierFull), string(runner.TierDeep):
		default:
			return nil, huma.Error422UnprocessableEntity(
				fmt.Sprintf("unknown tier %q: want one of smoke, full, deep", input.Body.Tier))
		}

		sk, err := skills.GetByName(ctx, input.Name)
		if err != nil {
			log.Error().Err(err).Str("skill", input.Name).Msg("evalqueue: get skill failed")
			return nil, huma.Error500InternalServerError("rerun eval: internal error")
		}
		if sk == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("skill %q not found", input.Name))
		}

		version := input.Body.Version
		if version <= 0 {
			version = sk.LatestVersion
		}
		if version == 0 {
			// A skill created but never published has LatestVersion == 0.
			// Enqueuing a job against version 0 would only defer a certain
			// failure to whatever a worker makes of it — refuse now, the same
			// way the sibling call sites in internal/skill/routes.go handle
			// an unpublished skill.
			return nil, huma.Error404NotFound(fmt.Sprintf("skill %q has no published version", input.Name))
		}
		if input.Body.Version > 0 {
			ver, err := skills.GetVersion(ctx, input.Name, input.Body.Version)
			if err != nil {
				log.Error().Err(err).Str("skill", input.Name).Int("version", input.Body.Version).Msg("evalqueue: get version failed")
				return nil, huma.Error500InternalServerError("rerun eval: internal error")
			}
			if ver == nil {
				return nil, huma.Error404NotFound(fmt.Sprintf("skill %q has no version %d", input.Name, input.Body.Version))
			}
		}

		suiteRef := input.Body.SuiteRef
		if suiteRef == "" {
			rec, err := suites.LatestForSkill(ctx, input.Name)
			switch {
			case errors.Is(err, evalsuite.ErrNotFound):
				// No suite registered. Submit with an empty ref and let the
				// worker derive one: it is the only process with an LLM key
				// and a Docker socket, and requiring an authored suite made
				// evaluation unreachable for every imported skill.
				suiteRef = ""
			case err != nil:
				log.Error().Err(err).Str("skill", input.Name).Msg("evalqueue: suite lookup failed")
				return nil, huma.Error500InternalServerError("rerun eval: internal error")
			default:
				// The whole point of storage: re-run against the skill's
				// *stored* suite, never a freshly generated one — a new
				// suite is a different measurement and the two scores would
				// not be comparable.
				suiteRef = rec.Ref
			}
		} else {
			// A caller-named ref must be validated: eval_suites.ref is
			// globally unique across skills and eval_jobs.suite_ref carries
			// no foreign key, so an unchecked ref would let a privileged
			// caller point this skill's job at another skill's tasks and
			// verifiers — a score attributed here that measures something
			// else entirely — or at a ref that does not exist at all, which
			// would otherwise surface only as a worker failure long after
			// this 202 was returned.
			rec, err := suites.Get(ctx, suiteRef)
			if err != nil {
				if errors.Is(err, evalsuite.ErrNotFound) {
					return nil, huma.Error404NotFound(fmt.Sprintf("suite %q not found", suiteRef))
				}
				log.Error().Err(err).Str("skill", input.Name).Str("suite_ref", suiteRef).Msg("evalqueue: suite lookup failed")
				return nil, huma.Error500InternalServerError("rerun eval: internal error")
			}
			if rec.SkillName != input.Name {
				return nil, huma.Error404NotFound(fmt.Sprintf("suite %q belongs to skill %q, not %q", suiteRef, rec.SkillName, input.Name))
			}
		}

		tier := input.Body.Tier
		if tier == "" {
			tier = "full"
		}

		requestedBy := ""
		if u != nil {
			requestedBy = u.Email
		}

		id, err := q.Submit(ctx, Job{
			SkillID:     sk.ID,
			SkillName:   sk.Name,
			Version:     version,
			SuiteRef:    suiteRef,
			Tier:        tier,
			Panel:       Panel{Agents: input.Body.Agents, Models: input.Body.Models},
			RequestedBy: requestedBy,
		})
		if err != nil {
			log.Error().Err(err).Str("skill", input.Name).Msg("evalqueue: rerun submit failed")
			return nil, huma.Error500InternalServerError("rerun eval: internal error")
		}

		return &rerunOutput{
			Status: http.StatusAccepted,
			Body:   rerunOutputBody{JobID: string(id)},
		}, nil
	})
}
