package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// EvalJob is the client-side representation of an eval queue job, as the
// claim/status endpoints return it.
type EvalJob struct {
	ID        string `json:"id"`
	SkillID   string `json:"skill_id"`
	SkillName string `json:"skill_name"`
	Version   int    `json:"version"`
	SuiteRef  string `json:"suite_ref"`
	Tier      string `json:"tier"`
	// Agents and Models are the requested panel — see evalqueue.Panel. The
	// whole point of the re-run endpoint is choosing a panel, so a wire
	// format that could not carry it back to the worker would silently
	// evaluate every job against the worker's default instead.
	Agents      []string `json:"agents,omitempty"`
	Models      []string `json:"models,omitempty"`
	Status      string   `json:"status"`
	Attempts    int      `json:"attempts"`
	MaxAttempts int      `json:"max_attempts"`
	WorkerID    string   `json:"worker_id,omitempty"`
	LastError   string   `json:"last_error,omitempty"`
	RequestedBy string   `json:"requested_by,omitempty"`
}

// ClaimEvalJob calls POST /api/eval/jobs/claim. It returns (nil, "", false,
// nil) when the server responds 204 — the queue is empty, not an error.
func (c *Client) ClaimEvalJob(workerID string, leaseSeconds int) (*EvalJob, string, bool, error) {
	payload, err := json.Marshal(struct {
		WorkerID     string `json:"worker_id"`
		LeaseSeconds int    `json:"lease_seconds,omitempty"`
	}{WorkerID: workerID, LeaseSeconds: leaseSeconds})
	if err != nil {
		return nil, "", false, fmt.Errorf("marshal claim eval job request: %w", err)
	}

	resp, err := c.do(http.MethodPost, "/api/eval/jobs/claim", bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, "", false, nil
	}

	var out struct {
		Job        EvalJob `json:"job"`
		ClaimToken string  `json:"claim_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, "", false, fmt.Errorf("decode claim eval job response: %w", err)
	}
	return &out.Job, out.ClaimToken, true, nil
}

// HeartbeatEvalJob calls POST /api/eval/jobs/{id}/heartbeat, extending the
// lease named by token.
func (c *Client) HeartbeatEvalJob(id, token string) error {
	resp, err := c.doHeaders(http.MethodPost, "/api/eval/jobs/"+url.PathEscape(id)+"/heartbeat",
		nil, "", map[string]string{"X-Claim-Token": token})
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// FailEvalJob calls POST /api/eval/jobs/{id}/fail, recording cause as the
// reason the job did not complete.
func (c *Client) FailEvalJob(id, token, cause string) error {
	payload, err := json.Marshal(struct {
		Error string `json:"error,omitempty"`
	}{Error: cause})
	if err != nil {
		return fmt.Errorf("marshal fail eval job request: %w", err)
	}

	resp, err := c.doHeaders(http.MethodPost, "/api/eval/jobs/"+url.PathEscape(id)+"/fail",
		bytes.NewReader(payload), "application/json", map[string]string{"X-Claim-Token": token})
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// PostEvalReport calls POST /api/eval/jobs/{id}/report with body, the JSON
// bytes of a completed eval report (see internal/eval/report.Report.Save).
func (c *Client) PostEvalReport(id, token string, body []byte) error {
	resp, err := c.doHeaders(http.MethodPost, "/api/eval/jobs/"+url.PathEscape(id)+"/report",
		bytes.NewReader(body), "application/json", map[string]string{"X-Claim-Token": token})
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// FetchEvalSuite calls GET /api/eval/suites/{ref} and returns the raw
// gzip-compressed suite archive.
func (c *Client) FetchEvalSuite(ref string) ([]byte, error) {
	resp, err := c.do(http.MethodGet, "/api/eval/suites/"+url.PathEscape(ref), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read eval suite response: %w", err)
	}
	return data, nil
}

// EvalSuiteMeta is the response to GET /api/eval/suites/{ref}/meta: the
// oracle-gate checks recorded for a suite, and the spec it was checked
// against (nil if the pusher didn't send one).
type EvalSuiteMeta struct {
	Checks      []EvalSuiteCheck `json:"checks"`
	SpecVersion int              `json:"spec_version"`
	Spec        json.RawMessage  `json:"spec,omitempty"`
}

// FetchEvalSuiteMeta calls GET /api/eval/suites/{ref}/meta and returns the
// oracle-gate checks and spec recorded for the suite. Both live on one call
// because every consumer of one needs the other: an eval cannot run without
// the checks, and a worker rebuilding a workspace from a downloaded bundle
// has no other source for the spec.
func (c *Client) FetchEvalSuiteMeta(ref string) (*EvalSuiteMeta, error) {
	resp, err := c.do(http.MethodGet, "/api/eval/suites/"+url.PathEscape(ref)+"/meta", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out EvalSuiteMeta
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode eval suite meta response: %w", err)
	}
	return &out, nil
}
