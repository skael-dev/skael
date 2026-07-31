package client

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/scan"
)

// Client communicates with the skael platform API. Every request includes the
// X-API-Key header for authentication.
type Client struct {
	endpoint   string
	apiKey     string
	httpClient *http.Client
}

// Skill is the client-side representation of a skill returned by the API.
type Skill struct {
	Name           string          `json:"name"`
	DisplayName    string          `json:"display_name,omitempty"`
	Description    string          `json:"description"`
	LatestVersion  int             `json:"latest_version"`
	Author         string          `json:"author"`
	License        string          `json:"license"`
	Compatibility  string          `json:"compatibility"`
	Tags           []string        `json:"tags"`
	SpecCompliance string          `json:"spec_compliance"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Frontmatter    json.RawMessage `json:"frontmatter"`
}

// Version is the client-side representation of a published skill version.
type Version struct {
	Version    int             `json:"version"`
	Checksum   string          `json:"checksum"`
	Changelog  string          `json:"changelog"`
	ScanResult json.RawMessage `json:"scan_result,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	Created    bool            `json:"created"`
	// Decision is the publish gate's verdict for this version. Always
	// present on a response from a gate-aware server; zero-value on an
	// older one (Outcome "" is treated the same as Allow by callers).
	Decision gate.Decision `json:"decision,omitempty"`
}

// ManifestEntry holds the sync metadata for a single skill.
type ManifestEntry struct {
	Name     string `json:"name"`
	Version  int    `json:"version"`
	Checksum string `json:"checksum"`
}

// APIError is returned when the server responds with a non-2xx status code.
type APIError struct {
	StatusCode int
	Message    string
	// Raw is the unparsed response body. Some endpoints attach a structured
	// payload — the publish endpoint embeds its scan report — that does not
	// survive being flattened into Message.
	Raw []byte
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Message)
}

// New creates a Client with a 30-second HTTP timeout.
func New(endpoint, apiKey string) *Client {
	return &Client{
		endpoint: endpoint,
		apiKey:   apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

const (
	// maxAttempts is the total number of tries, not retries. Four covers a
	// first sync of a large registry against a server that rate limits.
	maxAttempts = 4
	// maxRetryAfter caps how long a server can park the client. Beyond this,
	// failing fast is more useful than a silent multi-minute stall.
	maxRetryAfter = 60 * time.Second
)

// doWithRetry executes req up to maxAttempts times. It retries on connection
// errors, on 502/503/504, and on 429 — where it waits for the server's
// Retry-After if one is present and falls back to exponential backoff if not.
// The request body is rewound before each retry.
func (c *Client) doWithRetry(req *http.Request) (*http.Response, error) {
	var lastErr error
	var wait time.Duration

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if wait <= 0 {
				wait = time.Duration(1<<uint(attempt-1)) * time.Second
			}
			time.Sleep(wait)
			wait = 0

			// The previous attempt consumed the body. Without rewinding it the
			// retry would send an empty request and the server would reject it.
			if req.Body != nil {
				if req.GetBody == nil {
					return nil, fmt.Errorf("retry: request body cannot be replayed: %w", lastErr)
				}
				body, err := req.GetBody()
				if err != nil {
					return nil, fmt.Errorf("retry: rewind request body: %w", err)
				}
				req.Body = body
			}
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			wait = parseRetryAfter(resp.Header.Get("Retry-After"))
			resp.Body.Close()
			lastErr = fmt.Errorf("server returned 429 (rate limited)")
			continue
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			resp.Body.Close()
			lastErr = fmt.Errorf("server returned %d", resp.StatusCode)
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("after %d attempts: %w", maxAttempts, lastErr)
}

// parseRetryAfter reads a Retry-After header in either delta-seconds or
// HTTP-date form and clamps it to maxRetryAfter. It returns 0 when the header
// is missing, unparseable, or already in the past, which leaves the caller on
// plain exponential backoff.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}

	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return min(time.Duration(secs)*time.Second, maxRetryAfter)
	}

	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d <= 0 {
			return 0
		}
		return min(d, maxRetryAfter)
	}

	return 0
}

// do performs an HTTP request against the API, attaching the X-API-Key header.
// It returns the raw *http.Response so callers can decode the body themselves.
// On non-2xx responses it reads the body, attempts to extract a JSON "message"
// field, and returns an *APIError.
func (c *Client) do(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	return c.doHeaders(method, path, body, contentType, nil)
}

// doHeaders is do plus arbitrary extra request headers, needed for endpoints
// authenticated by something other than X-API-Key (the eval job endpoints
// carry a per-claim X-Claim-Token instead).
func (c *Client) doHeaders(method, path string, body io.Reader, contentType string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(method, c.endpoint+path, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.doWithRetry(req)
	if err != nil {
		return nil, fmt.Errorf("http %s %s: %w", method, path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)

		// Try to parse a Huma-style error envelope.
		var envelope struct {
			Title  string `json:"title"`
			Detail string `json:"detail"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		msg := string(raw)
		if json.Unmarshal(raw, &envelope) == nil {
			if envelope.Detail != "" {
				msg = envelope.Detail
			} else if envelope.Title != "" {
				msg = envelope.Title
			}
		}

		return nil, &APIError{StatusCode: resp.StatusCode, Message: msg, Raw: raw}
	}

	return resp, nil
}

// Health calls GET /api/health and returns an error if the server is not
// reachable or returns a non-ok status.
func (c *Client) Health() error {
	resp, err := c.do(http.MethodGet, "/api/health", nil, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ListSkills calls GET /api/skills?limit=&offset= and returns the slice of
// skills together with the total count reported by the server.
func (c *Client) ListSkills(limit, offset int) ([]Skill, int, error) {
	path := "/api/skills?" +
		"limit=" + url.QueryEscape(strconv.Itoa(limit)) +
		"&offset=" + url.QueryEscape(strconv.Itoa(offset))

	resp, err := c.do(http.MethodGet, path, nil, "")
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	var body struct {
		Skills []Skill `json:"skills"`
		Total  int     `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, fmt.Errorf("decode list skills response: %w", err)
	}
	return body.Skills, body.Total, nil
}

// GetSkill calls GET /api/skills/{name}. It returns (nil, nil) when the server
// responds with 404.
func (c *Client) GetSkill(name string) (*Skill, error) {
	resp, err := c.do(http.MethodGet, "/api/skills/"+url.PathEscape(name), nil, "")
	if err != nil {
		if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	defer resp.Body.Close()

	var sk Skill
	if err := json.NewDecoder(resp.Body).Decode(&sk); err != nil {
		return nil, fmt.Errorf("decode get skill response: %w", err)
	}
	return &sk, nil
}

// CreateSkill calls POST /api/skills to create a new skill record.
func (c *Client) CreateSkill(name, description string) (*Skill, error) {
	payload, err := json.Marshal(map[string]string{
		"name":        name,
		"description": description,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal create skill request: %w", err)
	}

	resp, err := c.do(http.MethodPost, "/api/skills", bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var sk Skill
	if err := json.NewDecoder(resp.Body).Decode(&sk); err != nil {
		return nil, fmt.Errorf("decode create skill response: %w", err)
	}
	return &sk, nil
}

// PublishVersion uploads archive (a gzip-compressed tar) to
// POST /api/skills/{name}/versions. Set override to publish despite blocking
// scan findings; the server accepts it only from an owner or admin and
// records it.
//
// On success it returns the new Version record, which carries its own
// Decision. When the server rejects the archive outright (a Block outcome,
// always 422) it returns the parsed scan report and decision alongside the
// error, so the caller can show the findings that actually blocked the
// publish instead of guessing.
func (c *Client) PublishVersion(name string, archive []byte, override bool) (*Version, *scan.Report, *gate.Decision, error) {
	path := "/api/skills/" + url.PathEscape(name) + "/versions"
	if override {
		path += "?override=true"
	}

	resp, err := c.do(http.MethodPost, path, bytes.NewReader(archive), "application/gzip")
	if err != nil {
		if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == http.StatusUnprocessableEntity {
			report, decision := parseGateDetail(apiErr.Raw)
			return nil, report, decision, err
		}
		return nil, nil, nil, err
	}
	defer resp.Body.Close()

	var ver Version
	if err := json.NewDecoder(resp.Body).Decode(&ver); err != nil {
		return nil, nil, nil, fmt.Errorf("decode publish version response: %w", err)
	}
	return &ver, nil, &ver.Decision, nil
}

// Review calls POST /api/skills/{name}/versions/{version}/review to approve
// or reject a version held by the publish gate. action must be "approve" or
// "reject"; the server enforces owner/admin privilege and rejects any other
// action. Returns the updated Version record.
func (c *Client) Review(name string, version int, action, reason string) (*Version, error) {
	payload, err := json.Marshal(struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}{Action: action, Reason: reason})
	if err != nil {
		return nil, fmt.Errorf("marshal review request: %w", err)
	}

	path := "/api/skills/" + url.PathEscape(name) + "/versions/" +
		url.PathEscape(strconv.Itoa(version)) + "/review"

	resp, err := c.do(http.MethodPost, path, bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var ver Version
	if err := json.NewDecoder(resp.Body).Decode(&ver); err != nil {
		return nil, fmt.Errorf("decode review response: %w", err)
	}
	return &ver, nil
}

// EvalSuiteCheck is one task's oracle-gate result, as the eval suite upload
// endpoint expects it.
type EvalSuiteCheck struct {
	TaskID string `json:"task_id"`
	OK     bool   `json:"ok"`
	Void   bool   `json:"void,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// EvalSuiteUpload is the response to a successful eval suite upload.
type EvalSuiteUpload struct {
	Ref       string `json:"ref"`
	TaskCount int    `json:"task_count"`
}

// UploadEvalSuite uploads an evaluation suite archive, together with the
// oracle-gate check results recorded for it and the spec it was checked
// against, to POST /api/eval/suites. archive is a gzip-compressed tar (see
// internal/evalsuite.PackDir); it is base64-encoded here because the
// endpoint's body is JSON, not raw bytes — unlike PublishVersion's skill
// archives, which travel as the request body itself. specJSON is the
// spec.SkillSpec that was checked, marshaled to JSON by the caller (may be
// nil) — a published bundle never carries spec.yaml, so this is the only
// channel a worker rebuilding a workspace from a downloaded bundle has to
// recover it.
func (c *Client) UploadEvalSuite(skill string, specVersion int, checks []EvalSuiteCheck, specJSON json.RawMessage, archive []byte) (*EvalSuiteUpload, error) {
	payload, err := json.Marshal(struct {
		Skill         string           `json:"skill"`
		SpecVersion   int              `json:"spec_version"`
		Checks        []EvalSuiteCheck `json:"checks"`
		Spec          json.RawMessage  `json:"spec,omitempty"`
		ArchiveBase64 string           `json:"archive_base64"`
	}{
		Skill:         skill,
		SpecVersion:   specVersion,
		Checks:        checks,
		Spec:          specJSON,
		ArchiveBase64: base64.StdEncoding.EncodeToString(archive),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal upload eval suite request: %w", err)
	}

	resp, err := c.do(http.MethodPost, "/api/eval/suites", bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out EvalSuiteUpload
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode upload eval suite response: %w", err)
	}
	return &out, nil
}

// parseGateDetail digs the scan report and gate decision out of a Huma error
// envelope. On a Block outcome the publish endpoint marshals
// {"scan": ..., "decision": ...} into the error's detail list, so it arrives
// as a JSON string inside errors[].message. Older servers (pre-gate) put the
// bare report there instead, so both shapes are accepted; the decision is nil
// in that case. Returns (nil, nil) when the body carries neither.
func parseGateDetail(raw []byte) (*scan.Report, *gate.Decision) {
	var envelope struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, nil
	}
	for _, e := range envelope.Errors {
		var wrapped struct {
			Scan     *scan.Report   `json:"scan"`
			Decision *gate.Decision `json:"decision"`
		}
		if json.Unmarshal([]byte(e.Message), &wrapped) == nil && wrapped.Scan != nil && wrapped.Scan.Status != "" {
			return wrapped.Scan, wrapped.Decision
		}
		var report scan.Report
		if json.Unmarshal([]byte(e.Message), &report) == nil && report.Status != "" {
			return &report, nil
		}
	}
	return nil, nil
}

// SearchSkills calls GET /api/search?q=&limit= and returns the matching skills.
func (c *Client) SearchSkills(query string, limit int) ([]Skill, error) {
	path := "/api/search?" +
		"q=" + url.QueryEscape(query) +
		"&limit=" + url.QueryEscape(strconv.Itoa(limit))

	resp, err := c.do(http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var body struct {
		Skills []Skill `json:"skills"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode search skills response: %w", err)
	}
	return body.Skills, nil
}

// ActivationSummary holds the activation analytics for a single skill.
type ActivationSummary struct {
	TotalCount    int            `json:"total_count"`
	UniqueDevs    int            `json:"unique_devs"`
	LastTriggered *time.Time     `json:"last_triggered"`
	ByAgent       map[string]int `json:"by_agent"`
}

// GetActivations calls GET /api/skills/{name}/activations and returns the
// activation summary for the given skill. Returns a zero-value summary when
// the server responds with 404.
func (c *Client) GetActivations(name string) (*ActivationSummary, error) {
	resp, err := c.do(http.MethodGet, "/api/skills/"+url.PathEscape(name)+"/activations", nil, "")
	if err != nil {
		if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == http.StatusNotFound {
			return &ActivationSummary{ByAgent: map[string]int{}}, nil
		}
		return nil, err
	}
	defer resp.Body.Close()

	var summary ActivationSummary
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		return nil, fmt.Errorf("decode activations response: %w", err)
	}
	if summary.ByAgent == nil {
		summary.ByAgent = map[string]int{}
	}
	return &summary, nil
}

// ListVersions calls GET /api/skills/{name}/versions and returns all versions
// for the skill in ascending order.
func (c *Client) ListVersions(name string) ([]Version, error) {
	resp, err := c.do(http.MethodGet, "/api/skills/"+url.PathEscape(name)+"/versions", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var body struct {
		Versions []Version `json:"versions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode list versions response: %w", err)
	}
	return body.Versions, nil
}

// GetManifest calls GET /api/sync/manifest and returns the list of manifest
// entries used for client-side sync diffing.
func (c *Client) GetManifest() ([]ManifestEntry, error) {
	resp, err := c.do(http.MethodGet, "/api/sync/manifest", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var entries []ManifestEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode manifest response: %w", err)
	}
	return entries, nil
}

// DownloadVersion calls GET /api/skills/{name}/versions/{v}/download and
// returns the raw archive bytes.
func (c *Client) DownloadVersion(name string, version int) ([]byte, error) {
	path := "/api/skills/" +
		url.PathEscape(name) +
		"/versions/" +
		url.PathEscape(strconv.Itoa(version)) +
		"/download"

	resp, err := c.do(http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read download response: %w", err)
	}
	return data, nil
}
