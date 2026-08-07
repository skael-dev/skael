package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
)

// HTTPAPI implements API over the skael HTTP surface, reusing cli/client's
// retry-with-backoff behaviour and 30s timeout rather than a bare
// http.Client (which has none).
type HTTPAPI struct {
	c *client.Client
}

// NewHTTPAPI builds an HTTPAPI talking to endpoint, authenticated with
// apiKey.
func NewHTTPAPI(endpoint, apiKey string) *HTTPAPI {
	return &HTTPAPI{c: client.New(endpoint, apiKey)}
}

// Claim calls POST /api/eval/jobs/claim.
func (h *HTTPAPI) Claim(_ context.Context, workerID string, lease time.Duration) (*evalqueue.Job, string, bool, error) {
	j, token, ok, err := h.c.ClaimEvalJob(workerID, int(lease.Seconds()))
	if err != nil {
		return nil, "", false, err
	}
	if !ok {
		return nil, "", false, nil
	}
	return &evalqueue.Job{
		ID:          evalqueue.JobID(j.ID),
		SkillID:     j.SkillID,
		SkillName:   j.SkillName,
		Version:     j.Version,
		SuiteRef:    j.SuiteRef,
		Tier:        j.Tier,
		Panel:       evalqueue.Panel{Agents: j.Agents, Models: j.Models},
		Status:      evalqueue.Status(j.Status),
		Attempts:    j.Attempts,
		MaxAttempts: j.MaxAttempts,
		WorkerID:    j.WorkerID,
		LastError:   j.LastError,
		RequestedBy: j.RequestedBy,
	}, token, true, nil
}

// Heartbeat calls POST /api/eval/jobs/{id}/heartbeat. A 409 response — the
// job is no longer running, or its lease already lapsed — is surfaced as
// evalqueue.ErrLeaseLost, exactly as the in-process queue's own Heartbeat
// does, so the worker's lease-lost handling does not need to know whether it
// is talking to Postgres directly or through HTTP.
//
// A 403 is treated the same way here, specifically for Heartbeat: the
// server's heartbeat handler (internal/evalqueue/routes.go) returns 403 for
// "the claim just doesn't verify", which covers both a forged token and the
// case where the job is still `running` with a technically-live lease but
// this worker's claim_token_hash no longer matches — the lapse-and-reclaim
// race. Treating only 409 as lease-lost left that branch heartbeating (and
// eventually trying to post a report) for a job this worker no longer owns;
// the server would reject the post, but the abandon-promptly property this
// package exists for would have silently failed in the meantime. FailJob and
// PostReport keep the narrower 409-only mapping: a 403 there is far more
// likely a genuinely invalid claim than a live reclaim race, and treating it
// as lease-lost buys nothing once the run has already finished.
func (h *HTTPAPI) Heartbeat(_ context.Context, id evalqueue.JobID, token string) error {
	err := h.c.HeartbeatEvalJob(string(id), token)
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusConflict || apiErr.StatusCode == http.StatusForbidden) {
		return evalqueue.ErrLeaseLost
	}
	return err
}

// FailJob calls POST /api/eval/jobs/{id}/fail.
func (h *HTTPAPI) FailJob(_ context.Context, id evalqueue.JobID, token, cause string) error {
	return asLeaseLost(h.c.FailEvalJob(string(id), token, cause))
}

// PostReport calls POST /api/eval/jobs/{id}/report with r's JSON encoding.
func (h *HTTPAPI) PostReport(_ context.Context, id evalqueue.JobID, token string, r *report.Report) error {
	var buf bytes.Buffer
	if err := r.Save(&buf); err != nil {
		return fmt.Errorf("worker: encode report: %w", err)
	}
	return asLeaseLost(h.c.PostEvalReport(string(id), token, buf.Bytes()))
}

// FetchSuite calls GET /api/eval/suites/{ref} and returns the raw archive.
func (h *HTTPAPI) FetchSuite(_ context.Context, ref string) ([]byte, error) {
	return h.c.FetchEvalSuite(ref)
}

// FetchBundle calls GET /api/skills/{name}/versions/{version}/download.
func (h *HTTPAPI) FetchBundle(_ context.Context, skill string, version int) ([]byte, error) {
	return h.c.DownloadVersion(skill, version)
}

// SuiteMeta calls GET /api/eval/suites/{ref}/meta.
func (h *HTTPAPI) SuiteMeta(_ context.Context, ref string) (SuiteMeta, error) {
	meta, err := h.c.FetchEvalSuiteMeta(ref)
	if err != nil {
		return SuiteMeta{}, err
	}
	checks := make([]evalsuite.Check, len(meta.Checks))
	for i, c := range meta.Checks {
		checks[i] = evalsuite.Check{TaskID: c.TaskID, OK: c.OK, Void: c.Void, Reason: c.Reason}
	}
	sp, err := unmarshalSuiteSpec(meta.Spec)
	if err != nil {
		return SuiteMeta{}, err
	}
	return SuiteMeta{Checks: checks, Spec: sp, Origin: evalsuite.Origin(meta.Origin)}, nil
}

// PushSuite uploads a derived suite through POST /api/eval/suites and returns
// its content-addressed ref. spec_version is 0: a derived spec was never
// saved to a workspace store, so it has no version to name. The job id and
// claim token are sent so the server can record the suite as derived at push
// time — see PushSuiteInput.
func (h *HTTPAPI) PushSuite(_ context.Context, in PushSuiteInput) (string, error) {
	wire := make([]client.EvalSuiteCheck, len(in.Checks))
	for i, c := range in.Checks {
		wire[i] = client.EvalSuiteCheck{TaskID: c.TaskID, OK: c.OK, Void: c.Void, Reason: c.Reason}
	}
	specJSON, err := json.Marshal(in.Spec)
	if err != nil {
		return "", fmt.Errorf("worker: marshal derived spec: %w", err)
	}
	out, err := h.c.UploadEvalSuite(client.EvalSuiteUploadRequest{
		Skill: in.Skill, SpecVersion: 0, Checks: wire, Spec: specJSON, Archive: in.Archive,
		JobID: string(in.JobID), ClaimToken: in.ClaimToken,
	})
	if err != nil {
		return "", err
	}
	return out.Ref, nil
}

// asLeaseLost converts a 409 Conflict — the server's signal that a claim no
// longer verifies (a cancelled job, or a lease another worker reclaimed) —
// into evalqueue.ErrLeaseLost, so callers can use errors.Is regardless of
// transport.
func asLeaseLost(err error) error {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
		return evalqueue.ErrLeaseLost
	}
	return err
}
