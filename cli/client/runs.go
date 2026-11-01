package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/skael-dev/skael/internal/gate"
)

// BatchResult is the answer to an event batch.
type BatchResult struct {
	Stored int `json:"stored"`
	Runs   int `json:"runs"`
}

// ReportEvents calls POST /api/events/batch. Each event passes through as the
// caller wrote it, so the server is the one place that validates an event.
func (c *Client) ReportEvents(events []json.RawMessage, idempotencyKey string) (*Response[BatchResult], error) {
	var headers map[string]string
	if idempotencyKey != "" {
		headers = map[string]string{"Idempotency-Key": idempotencyKey}
	}
	return fetchHeaders[BatchResult](c, http.MethodPost, "/api/events/batch", map[string]any{"events": events}, headers)
}

var batchLocationRe = regexp.MustCompile(`^body\.events\[(\d+)\]`)

// BatchFailure reads which event made a batch invalid. The batch route names
// the index itself, and the request schema names it in the error location.
func BatchFailure(err error) (index int, detail string, ok bool) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity {
		return 0, "", false
	}
	var body struct {
		Index  *int   `json:"index"`
		Detail string `json:"detail"`
		Errors []struct {
			Message  string `json:"message"`
			Location string `json:"location"`
		} `json:"errors"`
	}
	if json.Unmarshal(apiErr.Raw, &body) != nil {
		return 0, "", false
	}
	if body.Index != nil {
		return *body.Index, body.Detail, true
	}
	for _, e := range body.Errors {
		if m := batchLocationRe.FindStringSubmatch(e.Location); m != nil {
			if i, convErr := strconv.Atoi(m[1]); convErr == nil {
				return i, fmt.Sprintf("event %d: %s: %s", i, e.Location, e.Message), true
			}
		}
	}
	return 0, "", false
}

// OutcomeReport is the request of POST /api/outcomes.
type OutcomeReport struct {
	ExternalRunID string `json:"external_run_id"`
	Outcome       string `json:"outcome"`
	Repairs       int    `json:"repairs,omitempty"`
	ReviewVerdict string `json:"review_verdict,omitempty"`
	EvidenceURL   string `json:"evidence_url,omitempty"`
	Set           string `json:"set,omitempty"`
	ResolutionID  string `json:"resolution_id,omitempty"`
}

// RunRecord is a stored run.
type RunRecord struct {
	ID            string  `json:"id"`
	Consumer      string  `json:"consumer"`
	ExternalRunID string  `json:"external_run_id"`
	Outcome       *string `json:"outcome"`
	Repairs       int     `json:"repairs"`
}

// ReportOutcome calls POST /api/outcomes.
func (c *Client) ReportOutcome(report OutcomeReport) (*Response[RunRecord], error) {
	return fetch[RunRecord](c, http.MethodPost, "/api/outcomes", report)
}

// VersionOutcomes summarises one consumer's runs that loaded one version. The
// rates are nil below the sample the server needs.
type VersionOutcomes struct {
	Version        int      `json:"version"`
	Runs           int      `json:"runs"`
	Succeeded      int      `json:"succeeded"`
	Failed         int      `json:"failed"`
	Cancelled      int      `json:"cancelled"`
	NoOutcome      int      `json:"no_outcome"`
	SuccessRate    *float64 `json:"success_rate"`
	MeanRepairs    *float64 `json:"mean_repairs"`
	SampleTooSmall bool     `json:"sample_too_small"`
	Comparison     *struct {
		PreviousVersion   int     `json:"previous_version"`
		SuccessRateChange float64 `json:"success_rate_change"`
		MeanRepairsChange float64 `json:"mean_repairs_change"`
	} `json:"comparison"`
}

// SkillOutcomes is the outcome panel of one skill.
type SkillOutcomes struct {
	MinComparableRuns int `json:"min_comparable_runs"`
	Consumers         []struct {
		Consumer string            `json:"consumer"`
		Versions []VersionOutcomes `json:"versions"`
	} `json:"consumers"`
}

// GetSkillOutcomes calls GET /api/skills/{name}/outcomes.
func (c *Client) GetSkillOutcomes(name string) (*Response[SkillOutcomes], error) {
	return fetch[SkillOutcomes](c, http.MethodGet, "/api/skills/"+url.PathEscape(name)+"/outcomes", nil)
}

// SubmissionRequest is the request of POST /api/submissions.
type SubmissionRequest struct {
	Name          string `json:"name"`
	Rationale     string `json:"rationale"`
	RunLink       string `json:"run_link,omitempty"`
	ExternalRunID string `json:"external_run_id,omitempty"`
	ArchiveBase64 string `json:"archive_base64"`
}

// Submission is a skill archive awaiting or past a person's decision.
type Submission struct {
	ID        string    `json:"id"`
	SkillName string    `json:"skill_name"`
	Consumer  string    `json:"consumer"`
	State     string    `json:"state"`
	DecidedBy string    `json:"decided_by"`
	Reason    string    `json:"reason"`
	Version   *int      `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateSubmission calls POST /api/submissions. On a blocking finding it also
// returns the decision, so the caller can show what blocked the archive.
func (c *Client) CreateSubmission(req SubmissionRequest) (*Response[Submission], *gate.Decision, error) {
	res, err := fetch[Submission](c, http.MethodPost, "/api/submissions", req)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnprocessableEntity {
			_, decision := parseGateDetail(apiErr.Raw)
			return nil, decision, err
		}
		return nil, nil, err
	}
	return res, nil, nil
}

// ListSubmissions calls GET /api/submissions. An empty state lists all.
func (c *Client) ListSubmissions(state string) (*Response[struct {
	Submissions []Submission `json:"submissions"`
}], error) {
	path := "/api/submissions"
	if state != "" {
		path += "?state=" + url.QueryEscape(state)
	}
	return fetch[struct {
		Submissions []Submission `json:"submissions"`
	}](c, http.MethodGet, path, nil)
}

// GetSubmission calls GET /api/submissions/{id}.
func (c *Client) GetSubmission(id string) (*Response[Submission], error) {
	return fetch[Submission](c, http.MethodGet, "/api/submissions/"+url.PathEscape(id), nil)
}
