package contest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/skill"
)

// Submitter is the subset of evalqueue.Executor a contest needs.
type Submitter interface {
	Submit(ctx context.Context, j evalqueue.Job) (evalqueue.JobID, error)
}

// ClaimVerifier authenticates a worker posting a contest's runs. The claim
// token authenticates the claim rather than the caller's role, the same way
// an eval report is ingested.
type ClaimVerifier interface {
	VerifyClaim(ctx context.Context, id evalqueue.JobID, token string) (*evalqueue.Job, bool, error)
	Complete(ctx context.Context, id evalqueue.JobID, workerID string) error
}

// Wire shapes are prefixed with "contest" because Huma keys a generated schema
// off the Body field's type name, globally across packages, and a collision
// panics the server at startup.

type contestCandidateInput struct {
	Skill   string `json:"skill" minLength:"1"`
	Version int    `json:"version,omitempty"`
}

type contestCreateBody struct {
	Candidates []contestCandidateInput `json:"candidates" minItems:"2"`
	// SuiteRef names a stored suite. Omitted, the worker derives one from
	// every candidate, so no candidate's own claims set the bar.
	SuiteRef string `json:"suite_ref,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Attempts int    `json:"attempts,omitempty"`
}

type contestCreateInput struct {
	Body contestCreateBody
}

type contestCandidateOutput struct {
	Skill   string `json:"skill"`
	Version int    `json:"version"`
	Label   string `json:"label"`
}

type contestLossOutput struct {
	Label    string   `json:"label"`
	TaskID   string   `json:"task_id"`
	Missed   []string `json:"missed"`
	Evidence string   `json:"evidence,omitempty"`
}

// ContestOutput is the wire shape for a contest. Exported because Huma drops
// unexported embedded structs from a response body.
type ContestOutput struct {
	ID          string                   `json:"id"`
	Status      string                   `json:"status"`
	Outcome     string                   `json:"outcome,omitempty"`
	Winner      string                   `json:"winner,omitempty"`
	Detail      string                   `json:"detail,omitempty"`
	SuiteRef    string                   `json:"suite_ref,omitempty"`
	SuiteNote   string                   `json:"suite_note"`
	Tier        string                   `json:"tier"`
	Attempts    int                      `json:"attempts"`
	JobID       string                   `json:"job_id,omitempty"`
	ChainLink   bool                     `json:"chain_link"`
	Candidates  []contestCandidateOutput `json:"candidates"`
	Pairings    []Pairing                `json:"pairings,omitempty"`
	Losses      []contestLossOutput      `json:"losses,omitempty"`
	LastError   string                   `json:"last_error,omitempty"`
	RequestedBy string                   `json:"requested_by,omitempty"`
	CreatedAt   time.Time                `json:"created_at"`
	FinishedAt  *time.Time               `json:"finished_at,omitempty"`
}

type contestOutput struct {
	Status int
	Body   ContestOutput
}

type contestGetInput struct {
	ID string `path:"id"`
}

// contestReportBody carries each candidate's report, keyed by label. The
// worker sends measurements; the server decides. A verdict computed here
// cannot be forged by a worker and cannot drift from contest.Decide.
type contestReportBody struct {
	Reports map[string]json.RawMessage `json:"reports"`
}

type contestReportInput struct {
	ID         string `path:"id"`
	ClaimToken string `header:"X-Claim-Token"`
	Body       contestReportBody
}

type contestListInput struct {
	Name string `path:"name"`
}

type contestListBody struct {
	Contests []ContestOutput `json:"contests"`
}

type contestListOutput struct {
	Body contestListBody
}

func toOutput(c *Contest) ContestOutput {
	out := ContestOutput{
		ID: c.ID, Status: string(c.Status), Outcome: string(c.Outcome),
		Winner: c.Winner, Detail: c.Detail, SuiteRef: c.SuiteRef,
		SuiteNote: c.SuiteNote(), Tier: c.Tier, Attempts: c.Attempts,
		JobID: c.JobID, ChainLink: c.IsChainLink(), Pairings: c.Pairings,
		LastError: c.LastError, RequestedBy: c.RequestedBy,
		CreatedAt: c.CreatedAt, FinishedAt: c.FinishedAt,
	}
	for _, cand := range c.Candidates {
		out.Candidates = append(out.Candidates, contestCandidateOutput{
			Skill: cand.SkillName, Version: cand.Version, Label: cand.Label,
		})
	}
	for _, l := range c.Losses {
		out.Losses = append(out.Losses, contestLossOutput(l))
	}
	return out
}

// Label names a candidate in a verdict: skill@version, which is unambiguous
// across skills and stable in a stored result.
func Label(skillName string, version int) string {
	return fmt.Sprintf("%s@%d", skillName, version)
}

// ParseCandidate splits "skill@version". A missing version means the skill's
// released version, which the caller resolves.
func ParseCandidate(s string) (string, int, error) {
	name, ver, found := strings.Cut(strings.TrimSpace(s), "@")
	if name == "" {
		return "", 0, fmt.Errorf("contest: %q names no skill", s)
	}
	if !found || ver == "" {
		return name, 0, nil
	}
	var v int
	if _, err := fmt.Sscanf(ver, "%d", &v); err != nil || v < 1 {
		return "", 0, fmt.Errorf("contest: %q is not a version number", ver)
	}
	return name, v, nil
}

// RegisterRoutes wires the contest endpoints. A contest advises: nothing here
// releases a version, holds one, or clears a hold.
func RegisterRoutes(api huma.API, store *Store, skills *skill.Store, suites *evalsuite.Registry, queue Submitter, claims ClaimVerifier) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-contest",
		Method:        http.MethodPost,
		Path:          "/api/eval/contests",
		Summary:       "Run two or more candidate versions against one suite",
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *contestCreateInput) (*contestOutput, error) {
		user := auth.UserFromContext(ctx)

		c := &Contest{
			Tier:        strings.TrimSpace(input.Body.Tier),
			Attempts:    input.Body.Attempts,
			RequestedBy: user.Email,
		}
		if c.Tier == "" {
			c.Tier = "full"
		}
		if c.Attempts <= 0 {
			c.Attempts = DefaultAttempts
		}

		seen := map[string]bool{}
		for _, in := range input.Body.Candidates {
			cand, err := resolveCandidate(ctx, skills, in.Skill, in.Version)
			if err != nil {
				return nil, err
			}
			if seen[cand.Label] {
				return nil, huma.Error422UnprocessableEntity(
					fmt.Sprintf("create contest: %s is entered twice; a candidate cannot run against itself", cand.Label))
			}
			seen[cand.Label] = true
			c.Candidates = append(c.Candidates, *cand)
		}

		if err := resolveSuite(ctx, suites, c, input.Body.SuiteRef); err != nil {
			return nil, err
		}

		if err := store.Create(ctx, c); err != nil {
			log.Error().Err(err).Msg("contest: create failed")
			return nil, huma.Error500InternalServerError("create contest: internal error")
		}

		first := c.Candidates[0]
		jobID, err := queue.Submit(ctx, evalqueue.Job{
			SkillID: first.SkillID, SkillName: first.SkillName, Version: first.Version,
			SuiteRef: c.SuiteRef, Tier: c.Tier, RequestedBy: user.Email, ContestID: c.ID,
		})
		if err != nil {
			_ = store.Fail(ctx, c.ID, "could not enqueue: "+err.Error())
			log.Error().Err(err).Str("contest", c.ID).Msg("contest: enqueue failed")
			return nil, huma.Error500InternalServerError("create contest: could not enqueue the run")
		}
		if err := store.AttachJob(ctx, c.ID, string(jobID)); err != nil {
			log.Error().Err(err).Str("contest", c.ID).Msg("contest: attaching job failed")
			return nil, huma.Error500InternalServerError("create contest: internal error")
		}
		c.JobID, c.Status = string(jobID), StatusRunning

		return &contestOutput{Status: http.StatusAccepted, Body: toOutput(c)}, nil
	})

	registerReport(api, store, claims)

	huma.Register(api, huma.Operation{
		OperationID: "get-contest",
		Method:      http.MethodGet,
		Path:        "/api/eval/contests/{id}",
		Summary:     "Get a contest and its verdict",
	}, func(ctx context.Context, input *contestGetInput) (*contestOutput, error) {
		c, err := store.Get(ctx, input.ID)
		if err != nil {
			log.Error().Err(err).Msg("contest: get failed")
			return nil, huma.Error500InternalServerError("get contest: internal error")
		}
		if c == nil {
			return nil, huma.Error404NotFound("contest not found")
		}
		return &contestOutput{Status: http.StatusOK, Body: toOutput(c)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-skill-contests",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}/contests",
		Summary:     "List the contests a skill entered, newest first",
	}, func(ctx context.Context, input *contestListInput) (*contestListOutput, error) {
		sk, err := skills.GetByName(ctx, input.Name)
		if err != nil {
			return nil, huma.Error500InternalServerError("list contests: internal error", err)
		}
		if sk == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("skill %q not found", input.Name))
		}
		cs, err := store.ListForSkill(ctx, sk.ID)
		if err != nil {
			log.Error().Err(err).Msg("contest: list failed")
			return nil, huma.Error500InternalServerError("list contests: internal error")
		}
		out := contestListOutput{}
		out.Body.Contests = []ContestOutput{}
		for i := range cs {
			out.Body.Contests = append(out.Body.Contests, toOutput(&cs[i]))
		}
		return &out, nil
	})
}

func resolveCandidate(ctx context.Context, skills *skill.Store, name string, version int) (*Candidate, error) {
	sk, err := skills.GetByName(ctx, name)
	if err != nil {
		return nil, huma.Error500InternalServerError("create contest: internal error", err)
	}
	if sk == nil {
		return nil, huma.Error404NotFound(fmt.Sprintf("skill %q not found", name))
	}
	if version == 0 {
		// The released version, which is what latest_version points at. A held
		// version has a number and an archive but never advances that pointer.
		version = sk.LatestVersion
	}
	if version < 1 {
		return nil, huma.Error422UnprocessableEntity(
			fmt.Sprintf("create contest: %s has no released version to enter; name a version", name))
	}
	ver, err := skills.GetVersion(ctx, name, version)
	if err != nil {
		return nil, huma.Error500InternalServerError("create contest: internal error", err)
	}
	if ver == nil {
		return nil, huma.Error404NotFound(fmt.Sprintf("%s has no version %d", name, version))
	}
	return &Candidate{
		SkillID: sk.ID, SkillName: name, Version: version, Label: Label(name, version),
	}, nil
}

// resolveSuite records who chose the tasks. An empty ref leaves the suite to
// the worker, which derives one from every candidate.
func resolveSuite(ctx context.Context, suites *evalsuite.Registry, c *Contest, ref string) error {
	if strings.TrimSpace(ref) == "" {
		c.SuiteProvenance = ProvenanceDerivedAll
		return nil
	}
	rec, err := suites.Get(ctx, ref)
	if err != nil {
		return huma.Error500InternalServerError("create contest: internal error", err)
	}
	if rec == nil {
		return huma.Error404NotFound(fmt.Sprintf("eval suite %q not found", ref))
	}
	c.SuiteRef = rec.Ref
	if rec.Origin == evalsuite.OriginAuthored {
		c.SuiteProvenance, c.SuiteReviewedBy = ProvenanceAuthored, rec.ReviewedBy
		return nil
	}
	// A stored derived suite was generated from one skill's own claims, so it
	// grades the others against that skill. Allowed, and disclosed.
	c.SuiteProvenance, c.SuiteDerivedFrom = ProvenanceDerivedOne, rec.SkillName
	return nil
}

// registerReport wires the worker's report-back. Kept beside the other routes
// but separate for readability: it is the only one a worker calls.
func registerReport(api huma.API, store *Store, claims ClaimVerifier) {
	huma.Register(api, huma.Operation{
		OperationID: "report-contest",
		Method:      http.MethodPost,
		Path:        "/api/eval/contests/{id}/report",
		Summary:     "Post every candidate's runs and receive the verdict",
	}, func(ctx context.Context, input *contestReportInput) (*contestOutput, error) {
		c, err := store.Get(ctx, input.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("report contest: internal error", err)
		}
		if c == nil {
			return nil, huma.Error404NotFound("contest not found")
		}
		if c.JobID == "" {
			return nil, huma.Error409Conflict("report contest: this contest has no running job")
		}
		job, ok, err := claims.VerifyClaim(ctx, evalqueue.JobID(c.JobID), input.ClaimToken)
		if err != nil {
			return nil, huma.Error500InternalServerError("report contest: internal error", err)
		}
		if !ok {
			return nil, huma.Error403Forbidden("report contest: claim token does not match")
		}

		verdict, losses, err := decideReports(c, input.Body.Reports)
		if err != nil {
			_ = store.Fail(ctx, c.ID, err.Error())
			return nil, huma.Error422UnprocessableEntity("report contest: " + err.Error())
		}
		if err := store.Finish(ctx, c.ID, verdict, input.Body.Reports, losses); err != nil {
			log.Error().Err(err).Str("contest", c.ID).Msg("contest: storing the verdict failed")
			return nil, huma.Error500InternalServerError("report contest: internal error")
		}
		if err := claims.Complete(ctx, evalqueue.JobID(c.JobID), job.WorkerID); err != nil {
			log.Warn().Err(err).Str("contest", c.ID).Msg("contest: verdict stored but the job was not closed")
		}

		stored, err := store.Get(ctx, c.ID)
		if err != nil || stored == nil {
			return nil, huma.Error500InternalServerError("report contest: internal error", err)
		}
		return &contestOutput{Status: http.StatusOK, Body: toOutput(stored)}, nil
	})
}

// decideReports reduces every candidate's report to per-task results and
// decides. Every candidate must have reported: a verdict over a subset would
// name a winner that only ran because the others failed to.
func decideReports(c *Contest, reports map[string]json.RawMessage) (Verdict, []TaskLoss, error) {
	var outcomes []TaskOutcome
	var losses []TaskLoss
	for _, cand := range c.Candidates {
		raw, ok := reports[cand.Label]
		if !ok {
			return Verdict{}, nil, fmt.Errorf("no runs reported for %s", cand.Label)
		}
		rep, err := report.Load(bytes.NewReader(raw))
		if err != nil {
			return Verdict{}, nil, fmt.Errorf("report for %s: %w", cand.Label, err)
		}
		o, l := Reduce(cand.Label, rep)
		if len(o) == 0 {
			return Verdict{}, nil, fmt.Errorf("the report for %s measured no task", cand.Label)
		}
		outcomes = append(outcomes, o...)
		losses = append(losses, l...)
	}
	v, err := Decide(outcomes)
	if err != nil {
		return Verdict{}, nil, err
	}
	return v, losses, nil
}
