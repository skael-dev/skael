// Package api implements the LLM gateway against a direct Anthropic-compatible
// HTTP endpoint.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/llm"
)

// ErrNoAPIKey is returned when no key is configured.
var ErrNoAPIKey = errors.New("api: an API key is required")

const (
	defaultBaseURL = "https://api.anthropic.com"
	apiVersion     = "2023-06-01"
	defaultMaxTok  = 32768
	defaultTimeout = 3 * time.Minute
	// maxRetryAfter clamps a server-stated wait. A gateway under load can name
	// a wait longer than the run is worth.
	maxRetryAfter = 120 * time.Second
)

// AuthStyle selects how the API key is presented.
type AuthStyle string

const (
	AuthStyleAnthropic AuthStyle = "x-api-key"
	AuthStyleBearer    AuthStyle = "bearer"
)

// Options configures the gateway.
type Options struct {
	BaseURL     string
	APIKey      string
	StrongModel string
	FastModel   string
	AuthStyle   AuthStyle
	Cache       llm.Cache
	HTTPClient  *http.Client
	MaxRetries  int
	Sleep       func(time.Duration)
}

// Gateway is a direct-API LLM gateway.
type Gateway struct{ opts Options }

// New returns a gateway.
func New(o Options) (*Gateway, error) {
	if o.APIKey == "" {
		return nil, ErrNoAPIKey
	}
	if o.BaseURL == "" {
		o.BaseURL = defaultBaseURL
	}
	if o.StrongModel == "" {
		o.StrongModel = "claude-opus-5"
	}
	if o.FastModel == "" {
		o.FastModel = "claude-haiku-4-5-20251001"
	}
	if o.AuthStyle == "" {
		o.AuthStyle = AuthStyleAnthropic
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: defaultTimeout}
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	return &Gateway{opts: o}, nil
}

type request struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	Messages  []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type response struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete implements llm.Gateway.
func (g *Gateway) Complete(ctx context.Context, r llm.Req) (llm.Res, error) {
	key := llm.CacheKey(r)
	if g.opts.Cache != nil {
		if v, ok, err := g.opts.Cache.Get(key); err == nil && ok {
			return llm.Res{Text: v, Model: g.modelFor(r.ModelClass), Cached: true}, nil
		}
	}

	var lastErr error
	var wait time.Duration
	for attempt := 0; attempt <= g.opts.MaxRetries; attempt++ {
		if attempt > 0 {
			if ctx.Err() != nil {
				return llm.Res{}, lastErr
			}
			g.opts.Sleep(jitter(wait))
		}

		res, retryable, retryAfter, err := g.post(ctx, r)
		if err == nil {
			if g.opts.Cache != nil {
				_ = g.opts.Cache.Put(key, res.Text)
			}
			return res, nil
		}
		lastErr = err
		if !retryable || ctx.Err() != nil {
			return llm.Res{}, err
		}
		wait = backoff(attempt, retryAfter)
	}
	return llm.Res{}, fmt.Errorf("api: giving up after %d retries: %w", g.opts.MaxRetries, lastErr)
}

// backoff picks the wait before the next attempt. A server-stated retry-after
// wins over the doubled ladder, clamped to maxRetryAfter. attempt is 0-based.
func backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > maxRetryAfter {
			return maxRetryAfter
		}
		return retryAfter
	}
	return time.Duration(1<<attempt) * time.Second
}

// jitter spreads concurrent callers by plus or minus 20%, so a shared rate
// limit does not wake every judge call at the same instant.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	span := float64(d) * 0.4
	return time.Duration(float64(d)*0.8 + rand.Float64()*span)
}

// parseRetryAfter reads the header as whole seconds. Anthropic states it that
// way; an HTTP-date form is ignored rather than guessed at.
func parseRetryAfter(h string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil || secs < 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

func (g *Gateway) post(ctx context.Context, r llm.Req) (llm.Res, bool, time.Duration, error) {
	prompt := r.Prompt
	if len(r.Schema) > 0 {
		prompt += "\n\nReply with JSON only, validating against this schema:\n" + string(r.Schema)
	}

	body, err := json.Marshal(request{
		Model:     g.modelFor(r.ModelClass),
		MaxTokens: defaultMaxTok,
		Messages:  []message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return llm.Res{}, false, 0, fmt.Errorf("api: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(g.opts.BaseURL, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return llm.Res{}, false, 0, fmt.Errorf("api: request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	// anthropic-version is sent regardless of auth style: compatible gateways
	// accept it and Anthropic requires it.
	req.Header.Set("anthropic-version", apiVersion)
	switch g.opts.AuthStyle {
	case AuthStyleBearer:
		req.Header.Set("Authorization", "Bearer "+g.opts.APIKey)
	default:
		req.Header.Set("x-api-key", g.opts.APIKey)
	}

	resp, err := g.opts.HTTPClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.Res{}, false, 0, fmt.Errorf("api: timeout: no response within %s: %w", g.opts.HTTPClient.Timeout, llm.ErrTimeout)
		}
		return llm.Res{}, true, 0, fmt.Errorf("api: post: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return llm.Res{}, true, 0, fmt.Errorf("api: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var parsed response
		_ = json.Unmarshal(raw, &parsed)
		msg := strings.TrimSpace(string(raw))
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		retryable := resp.StatusCode >= http.StatusInternalServerError || resp.StatusCode == http.StatusTooManyRequests
		return llm.Res{}, retryable, parseRetryAfter(resp.Header.Get("retry-after")),
			fmt.Errorf("api: %d: %s", resp.StatusCode, msg)
	}

	var parsed response
	if uerr := json.Unmarshal(raw, &parsed); uerr != nil {
		return llm.Res{}, true, 0, fmt.Errorf("api: malformed response body: %w (body: %.200s)", uerr, raw)
	}

	var sb strings.Builder
	for _, b := range parsed.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	if parsed.StopReason == "max_tokens" {
		return llm.Res{}, false, 0, fmt.Errorf("api: response truncated at max_tokens (%d); raise the cap or shorten the request", defaultMaxTok)
	}
	if sb.Len() == 0 {
		return llm.Res{}, false, 0, fmt.Errorf("api: response contained no text content blocks (body: %.200s)", raw)
	}
	return llm.Res{Text: sb.String(), Model: parsed.Model}, false, 0, nil
}

func (g *Gateway) modelFor(c llm.ModelClass) string {
	if c == llm.ClassFast {
		return g.opts.FastModel
	}
	return g.opts.StrongModel
}

// ModelFor implements llm.Gateway.
func (g *Gateway) ModelFor(c llm.ModelClass) string {
	return g.modelFor(c)
}
