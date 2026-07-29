package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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
	req, err := http.NewRequest(method, c.endpoint+path, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
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

		return nil, &APIError{StatusCode: resp.StatusCode, Message: msg}
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
// POST /api/skills/{name}/versions.
//
// On success it returns the new Version record.
// On 422 (critical security scan) it returns (nil, scanBody, err) where
// scanBody is the raw JSON scan report embedded in the error response.
func (c *Client) PublishVersion(name string, archive []byte) (*Version, json.RawMessage, error) {
	resp, err := c.do(
		http.MethodPost,
		"/api/skills/"+url.PathEscape(name)+"/versions",
		bytes.NewReader(archive),
		"application/gzip",
	)
	if err != nil {
		if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == http.StatusUnprocessableEntity {
			// The raw scan JSON is embedded in the error message by the server.
			// Try to recover it as-is; fall back to the plain message string.
			var scanBody json.RawMessage
			if json.Unmarshal([]byte(apiErr.Message), &scanBody) == nil {
				return nil, scanBody, err
			}
			// The server may wrap the scan JSON inside a detail/errors field — hand
			// back whatever we have as a JSON string so callers always receive
			// valid JSON.
			scanBody = json.RawMessage(strconv.Quote(apiErr.Message))
			return nil, scanBody, err
		}
		return nil, nil, err
	}
	defer resp.Body.Close()

	var ver Version
	if err := json.NewDecoder(resp.Body).Decode(&ver); err != nil {
		return nil, nil, fmt.Errorf("decode publish version response: %w", err)
	}
	return &ver, nil, nil
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
