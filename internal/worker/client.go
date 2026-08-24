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

// HTTPAPI implements API over the skael HTTP surface.
type HTTPAPI struct {
	c *client.Client
}

// NewHTTPAPI builds an HTTPAPI.
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

// Heartbeat calls POST /api/eval/jobs/{id}/heartbeat. Both 409 (job no longer
// running) and 403 (claim does not verify, including a lapse-and-reclaim
// race) map to ErrLeaseLost.
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
	sp, err := unmarshalSuiteSpec(meta.Spec)
	if err != nil {
		return SuiteMeta{}, err
	}
	return SuiteMeta{
		Spec:             sp,
		Origin:           evalsuite.Origin(meta.Origin),
		MachineGenerated: meta.MachineGenerated,
	}, nil
}

// PushSuite uploads a derived suite and returns its content-addressed ref.
func (h *HTTPAPI) PushSuite(_ context.Context, in PushSuiteInput) (string, error) {
	specJSON, err := json.Marshal(in.Spec)
	if err != nil {
		return "", fmt.Errorf("worker: marshal derived spec: %w", err)
	}
	out, err := h.c.UploadEvalSuite(client.EvalSuiteUploadRequest{
		Skill: in.Skill, SpecVersion: 0, Spec: specJSON, Archive: in.Archive,
		JobID: string(in.JobID), ClaimToken: in.ClaimToken,
	})
	if err != nil {
		return "", err
	}
	return out.Ref, nil
}

// asLeaseLost maps a 409 Conflict to ErrLeaseLost.
func asLeaseLost(err error) error {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
		return evalqueue.ErrLeaseLost
	}
	return err
}
