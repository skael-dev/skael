package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// ContestCandidate is one entry in a contest request or result.
type ContestCandidate struct {
	Skill   string `json:"skill"`
	Version int    `json:"version,omitempty"`
	Label   string `json:"label,omitempty"`
}

// ContestTask is one task's head-to-head result.
type ContestTask struct {
	TaskID string  `json:"task_id"`
	A      string  `json:"a"`
	B      string  `json:"b"`
	ARate  float64 `json:"a_rate"`
	BRate  float64 `json:"b_rate"`
	Winner string  `json:"winner"`
}

// ContestPairing is the result between two candidates.
type ContestPairing struct {
	A       string        `json:"a"`
	B       string        `json:"b"`
	AWins   int           `json:"a_wins"`
	BWins   int           `json:"b_wins"`
	Ties    int           `json:"ties"`
	PValue  float64       `json:"p_value"`
	Outcome string        `json:"outcome"`
	Winner  string        `json:"winner"`
	Tasks   []ContestTask `json:"tasks"`
}

// ContestLoss is one task a candidate lost, and what it missed.
type ContestLoss struct {
	Label    string   `json:"label"`
	TaskID   string   `json:"task_id"`
	Missed   []string `json:"missed"`
	Evidence string   `json:"evidence,omitempty"`
}

// Contest is a contest and, once it finishes, its verdict.
type Contest struct {
	ID         string             `json:"id"`
	Status     string             `json:"status"`
	Outcome    string             `json:"outcome"`
	Winner     string             `json:"winner"`
	Detail     string             `json:"detail"`
	SuiteRef   string             `json:"suite_ref"`
	SuiteNote  string             `json:"suite_note"`
	Tier       string             `json:"tier"`
	Attempts   int                `json:"attempts"`
	JobID      string             `json:"job_id"`
	ChainLink  bool               `json:"chain_link"`
	Candidates []ContestCandidate `json:"candidates"`
	Pairings   []ContestPairing   `json:"pairings"`
	Losses     []ContestLoss      `json:"losses"`
	LastError  string             `json:"last_error"`
}

// CreateContest queues a contest between candidates.
func (c *Client) CreateContest(candidates []ContestCandidate, suiteRef, tier string, attempts int) (*Contest, error) {
	payload, err := json.Marshal(struct {
		Candidates []ContestCandidate `json:"candidates"`
		SuiteRef   string             `json:"suite_ref,omitempty"`
		Tier       string             `json:"tier,omitempty"`
		Attempts   int                `json:"attempts,omitempty"`
	}{Candidates: candidates, SuiteRef: suiteRef, Tier: tier, Attempts: attempts})
	if err != nil {
		return nil, fmt.Errorf("marshal contest request: %w", err)
	}

	resp, err := c.do(http.MethodPost, "/api/eval/contests", bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out Contest
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode contest response: %w", err)
	}
	return &out, nil
}

// GetContest fetches a contest and its verdict.
func (c *Client) GetContest(id string) (*Contest, error) {
	resp, err := c.do(http.MethodGet, "/api/eval/contests/"+url.PathEscape(id), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out Contest
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode contest: %w", err)
	}
	return &out, nil
}

// ListContests returns the contests a skill entered, newest first.
func (c *Client) ListContests(name string) ([]Contest, error) {
	resp, err := c.do(http.MethodGet, "/api/skills/"+url.PathEscape(name)+"/contests", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var body struct {
		Contests []Contest `json:"contests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode contests: %w", err)
	}
	return body.Contests, nil
}
