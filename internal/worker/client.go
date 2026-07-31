package worker

import (
	"bytes"
	"context"
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
		Status:      evalqueue.Status(j.Status),
		Attempts:    j.Attempts,
		MaxAttempts: j.MaxAttempts,
		WorkerID:    j.WorkerID,
		LastError:   j.LastError,
		RequestedBy: j.RequestedBy,
	}, token, true, nil
}

// Heartbeat calls POST /api/eval/jobs/{id}/heartbeat. A 409 response — the
// claim no longer verifies — is surfaced as evalqueue.ErrLeaseLost, exactly
// as the in-process queue's own Heartbeat does, so the worker's lease-lost
// handling does not need to know whether it is talking to Postgres directly
// or through HTTP.
func (h *HTTPAPI) Heartbeat(_ context.Context, id evalqueue.JobID, token string) error {
	return asLeaseLost(h.c.HeartbeatEvalJob(string(id), token))
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

// SuiteChecks calls GET /api/eval/suites/{ref}/checks.
func (h *HTTPAPI) SuiteChecks(_ context.Context, ref string) ([]evalsuite.Check, error) {
	checks, err := h.c.FetchEvalSuiteChecks(ref)
	if err != nil {
		return nil, err
	}
	out := make([]evalsuite.Check, len(checks))
	for i, c := range checks {
		out[i] = evalsuite.Check{TaskID: c.TaskID, OK: c.OK, Void: c.Void, Reason: c.Reason}
	}
	return out, nil
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
