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

// RunEvalRequest is one queued eval. A zero version evaluates the latest, and
// an empty tier is the deepest tier the skill's suite fills. Set installs a
// set beside the skill; SetResolution pins one stored resolution of it.
type RunEvalRequest struct {
	Version       int    `json:"version,omitempty"`
	Tier          string `json:"tier,omitempty"`
	Set           string `json:"set,omitempty"`
	SetResolution string `json:"set_resolution,omitempty"`
}

// QueuedEval is the queued job, its tier and the set resolution it pinned.
type QueuedEval struct {
	JobID string      `json:"job_id"`
	Tier  string      `json:"tier"`
	Set   *EvalJobSet `json:"set,omitempty"`
}

// RunEval calls POST /api/skills/{name}/evals.
func (c *Client) RunEval(name string, req RunEvalRequest) (*QueuedEval, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(http.MethodPost, "/api/skills/"+url.PathEscape(name)+"/evals", bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out QueuedEval
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode eval response: %w", err)
	}
	return &out, nil
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
	// Report is the full report: null when the version is held and the
	// caller is not an owner or an admin.
	Report *struct {
		Set *EvalSetResult `json:"set,omitempty"`
	} `json:"report,omitempty"`
}

// EvalSetResult is what a set's members did beside the skill.
type EvalSetResult struct {
	Name         string `json:"name"`
	ResolutionID string `json:"resolution_id"`
	Shares       []struct {
		Name   string  `json:"name"`
		Took   int     `json:"took"`
		Shared int     `json:"shared"`
		Of     int     `json:"of"`
		Share  float64 `json:"share"`
	} `json:"shares"`
	RulesLost []struct {
		TaskID      string `json:"task_id"`
		Text        string `json:"text"`
		Check       bool   `json:"check"`
		AlonePasses int    `json:"alone_passes"`
		AloneRuns   int    `json:"alone_runs"`
		SetPasses   int    `json:"set_passes"`
		SetRuns     int    `json:"set_runs"`
	} `json:"rules_lost"`
	TokensMedian int64 `json:"tokens_median"`
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
