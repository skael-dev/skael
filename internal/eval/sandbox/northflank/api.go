package northflank

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// defaultBase is the Northflank REST API root. newClient sets it unless a
// test has already substituted an httptest.Server URL onto client.base.
const defaultBase = "https://api.northflank.com/v1"

// ownerLabelKey marks every service this driver creates. listSandboxes
// filters on it, which is what lets a sweep find and reclaim an orphaned
// sandbox without touching anything else in the project.
const ownerLabelKey = "skael.owner"

// client is a thin REST wrapper over the Northflank API. It carries no
// retry or backoff logic of its own; the caller decides how to react to an
// error.
type client struct {
	o    Options
	http *http.Client
	base string
}

func newClient(o Options) *client {
	return &client{o: o, http: &http.Client{}, base: defaultBase}
}

// sandboxRef identifies a service in a list response.
type sandboxRef struct {
	ID   string
	Name string
}

// apiError carries the provider's own message, so a quota or plan-limit
// refusal reads differently from a skill that failed its task. It never
// carries the request that produced it, which is what keeps the token out
// of the string.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("northflank: request failed (%d): %s", e.status, e.message)
}

// do sends one request and decodes a 2xx JSON response into out. body is
// marshaled as the request body when non-nil; out is left untouched when
// nil, which is how deleteSandbox uses this without a response shape.
func (c *client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("northflank: encoding request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return fmt.Errorf("northflank: building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.o.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("northflank: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("northflank: reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		message := resp.Status
		if json.Unmarshal(respBody, &envelope) == nil && envelope.Error.Message != "" {
			message = envelope.Error.Message
		}
		return &apiError{status: resp.StatusCode, message: message}
	}

	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("northflank: decoding response: %w", err)
	}
	return nil
}

// createSandbox creates a deployment service running o.Image on o.Plan and
// returns its service id. The owner label lets a later sweep find it.
func (c *client) createSandbox(ctx context.Context, name string, env []string) (string, error) {
	external := map[string]any{"imagePath": c.o.Image}
	if c.o.RegistryCredential != "" {
		external["credentials"] = c.o.RegistryCredential
	}

	reqBody := map[string]any{
		"name":    name,
		"billing": map[string]any{"deploymentPlan": c.o.Plan},
		"deployment": map[string]any{
			"instances": 1,
			"docker":    map[string]any{"configType": "default"},
			"external":  external,
		},
		"runtimeEnvironment": envMap(env),
		"metadata": map[string]any{
			"labels": map[string]string{ownerLabelKey: "skael-worker"},
		},
	}

	var resp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	path := fmt.Sprintf("/projects/%s/services", c.o.Project)
	if err := c.do(ctx, http.MethodPost, path, reqBody, &resp); err != nil {
		return "", err
	}
	return resp.Data.ID, nil
}

// envMap turns a "KEY=VALUE" slice, the shape the rest of the driver package
// passes around, into the map Northflank's runtimeEnvironment expects. An
// entry without "=" is skipped rather than sent malformed.
func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		m[key] = value
	}
	return m
}

// sandboxRunning reports whether the service's deployment has completed
// rollout. A build or scaling service answers false, which is correct: the
// driver has nothing to exec into yet.
func (c *client) sandboxRunning(ctx context.Context, id string) (bool, error) {
	var resp struct {
		Data struct {
			Status struct {
				Deployment struct {
					Status string `json:"status"`
				} `json:"deployment"`
			} `json:"status"`
		} `json:"data"`
	}
	path := fmt.Sprintf("/projects/%s/services/%s", c.o.Project, id)
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return false, err
	}
	return resp.Data.Status.Deployment.Status == "COMPLETED", nil
}

// execSandbox runs argv inside the running service and returns its exit
// code. A non-zero code is not an error here, matching the docker and
// kubernetes drivers: only a transport or provider failure returns err.
func (c *client) execSandbox(ctx context.Context, id string, argv []string, stdout, stderr io.Writer) (int, error) {
	reqBody := map[string]any{"command": argv}

	var resp struct {
		Data struct {
			CommandResult struct {
				ExitCode int `json:"exitCode"`
			} `json:"commandResult"`
			StdOut string `json:"stdOut"`
			StdErr string `json:"stdErr"`
		} `json:"data"`
	}
	path := fmt.Sprintf("/projects/%s/services/%s/commands/exec", c.o.Project, id)
	if err := c.do(ctx, http.MethodPost, path, reqBody, &resp); err != nil {
		return 0, err
	}
	if _, err := io.WriteString(stdout, resp.Data.StdOut); err != nil {
		return 0, fmt.Errorf("northflank: writing stdout: %w", err)
	}
	if _, err := io.WriteString(stderr, resp.Data.StdErr); err != nil {
		return 0, fmt.Errorf("northflank: writing stderr: %w", err)
	}
	return resp.Data.CommandResult.ExitCode, nil
}

// deleteSandbox removes the service. A 404 is treated as success: a delete
// that races another delete, or a sweep re-checking a service that already
// stopped, must not surface as a failure.
func (c *client) deleteSandbox(ctx context.Context, id string) error {
	path := fmt.Sprintf("/projects/%s/services/%s", c.o.Project, id)
	err := c.do(ctx, http.MethodDelete, path, nil, nil)
	var ae *apiError
	if errors.As(err, &ae) && ae.status == http.StatusNotFound {
		return nil
	}
	return err
}

// listSandboxes returns every service this driver owns, identified by
// ownerLabelKey. Anything else in the project — even a service the same
// token can see — is filtered out.
func (c *client) listSandboxes(ctx context.Context) ([]sandboxRef, error) {
	var resp struct {
		Data struct {
			Services []struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Metadata struct {
					Labels map[string]string `json:"labels"`
				} `json:"metadata"`
			} `json:"services"`
		} `json:"data"`
	}
	path := fmt.Sprintf("/projects/%s/services", c.o.Project)
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}

	var refs []sandboxRef
	for _, s := range resp.Data.Services {
		if _, ok := s.Metadata.Labels[ownerLabelKey]; !ok {
			continue
		}
		refs = append(refs, sandboxRef{ID: s.ID, Name: s.Name})
	}
	return refs, nil
}
