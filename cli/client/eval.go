package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// RunEval calls POST /api/skills/{name}/evals and returns the queued job's id
// and tier. A zero version evaluates the latest, and an empty tier is the
// deepest tier the skill's suite fills.
func (c *Client) RunEval(name string, version int, tier string) (string, string, error) {
	payload, err := json.Marshal(struct {
		Version int    `json:"version,omitempty"`
		Tier    string `json:"tier,omitempty"`
	}{version, tier})
	if err != nil {
		return "", "", err
	}
	resp, err := c.do(http.MethodPost, "/api/skills/"+url.PathEscape(name)+"/evals", bytes.NewReader(payload), "application/json")
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var out struct {
		JobID string `json:"job_id"`
		Tier  string `json:"tier"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", fmt.Errorf("decode eval response: %w", err)
	}
	return out.JobID, out.Tier, nil
}

// GetEvalJob calls GET /api/eval/jobs/{id}.
func (c *Client) GetEvalJob(id string) (*EvalJob, error) {
	resp, err := c.do(http.MethodGet, "/api/eval/jobs/"+url.PathEscape(id), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out EvalJob
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode eval job: %w", err)
	}
	return &out, nil
}

// EvalJobEvent is one progress record of an eval job.
type EvalJobEvent struct {
	ID           int64     `json:"id"`
	At           time.Time `json:"at"`
	Kind         string    `json:"kind"`
	Phase        string    `json:"phase,omitempty"`
	Total        int       `json:"total,omitempty"`
	Task         string    `json:"task,omitempty"`
	Condition    string    `json:"condition,omitempty"`
	Model        string    `json:"model,omitempty"`
	Attempt      int       `json:"attempt,omitempty"`
	Status       string    `json:"status,omitempty"`
	OK           *bool     `json:"ok,omitempty"`
	DurationMS   int64     `json:"duration_ms,omitempty"`
	InputTokens  int64     `json:"input_tokens,omitempty"`
	OutputTokens int64     `json:"output_tokens,omitempty"`
	CostUSD      float64   `json:"cost_usd,omitempty"`
	Error        string    `json:"error,omitempty"`
	Actor        string    `json:"actor,omitempty"`
}

// EvalJobProgress is a job's summary and its events after a cursor.
type EvalJobProgress struct {
	Job *struct {
		Status    string  `json:"status"`
		Phase     string  `json:"phase"`
		Total     int     `json:"sessions_total"`
		Done      int     `json:"sessions_done"`
		Failed    int     `json:"sessions_failed"`
		CostUSD   float64 `json:"cost_usd"`
		LastError string  `json:"last_error"`
		Stalled   bool    `json:"stalled"`
	} `json:"job"`
	Events []EvalJobEvent `json:"events"`
}

// EvalJobEvents calls GET /api/eval/jobs/{id}/events?after=N.
func (c *Client) EvalJobEvents(id string, after int64) (*EvalJobProgress, error) {
	resp, err := c.do(http.MethodGet, "/api/eval/jobs/"+url.PathEscape(id)+"/events?after="+strconv.FormatInt(after, 10), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out EvalJobProgress
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode eval job events: %w", err)
	}
	return &out, nil
}

// QualityScore is the part of a version's quality record the CLI prints.
type QualityScore struct {
	Version       int      `json:"version"`
	Headline      float64  `json:"headline_score"`
	Baseline      *float64 `json:"baseline,omitempty"`
	Lift          *float64 `json:"lift,omitempty"`
	PanelComplete bool     `json:"panel_complete"`
	SuiteDerived  bool     `json:"suite_derived"`
	SuiteRef      string   `json:"suite_ref"`
	Tier          string   `json:"tier"`
	JobID         string   `json:"job_id,omitempty"`
}

// QualityForVersion calls GET /api/skills/{name}/quality/{version}.
func (c *Client) QualityForVersion(name string, version int) (*QualityScore, error) {
	resp, err := c.do(http.MethodGet, "/api/skills/"+url.PathEscape(name)+"/quality/"+strconv.Itoa(version), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out QualityScore
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode quality: %w", err)
	}
	return &out, nil
}
