package northflank

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(t *testing.T, h http.HandlerFunc) *client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := newClient(validOptions().withDefaults())
	c.base = srv.URL
	return c
}

func TestCreateSandbox_SendsTheTokenAsABearerCredential(t *testing.T) {
	var gotAuth string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "svc-1"}})
	})
	if _, err := c.createSandbox(context.Background(), "whetstone-run-abc", nil); err != nil {
		t.Fatalf("createSandbox: %v", err)
	}
	if gotAuth != "Bearer nf_test" {
		t.Errorf("Authorization = %q, want a bearer token", gotAuth)
	}
}

// The image, the plan and the owner label must all reach the provider: a
// missing label makes the sweep unable to find an orphan, which is a bill.
func TestCreateSandbox_CarriesTheImageThePlanAndTheOwnerLabel(t *testing.T) {
	var body map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "svc-1"}})
	})
	if _, err := c.createSandbox(context.Background(), "whetstone-run-abc", []string{"FOO=bar"}); err != nil {
		t.Fatalf("createSandbox: %v", err)
	}
	raw, _ := json.Marshal(body)
	for _, want := range []string{"whetstone-base", defaultPlan, ownerLabelKey, "FOO"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("create body does not carry %q:\n%s", want, raw)
		}
	}
}

// A provider error must never be mistaken for a skill's failure.
func TestCreateSandbox_ReportsAQuotaRefusalAsAnError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"error":{"message":"plan resource limit reached"}}`)
	})
	_, err := c.createSandbox(context.Background(), "whetstone-run-abc", nil)
	if err == nil {
		t.Fatal("createSandbox: want an error")
	}
	if !strings.Contains(err.Error(), "plan resource limit reached") {
		t.Errorf("error %q must carry the provider's own message", err)
	}
}

func TestExecSandbox_ReturnsTheCommandsExitCodeAsAResult(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"commandResult": map[string]any{"exitCode": 3}, "stdOut": "out", "stdErr": "err"},
		})
	})
	var out, errBuf strings.Builder
	code, err := c.execSandbox(context.Background(), "svc-1", []string{"sh", "-c", "exit 3"}, &out, &errBuf)
	if err != nil {
		t.Fatalf("execSandbox: %v", err)
	}
	if code != 3 {
		t.Errorf("code = %d, want 3; a non-zero exit is a result, not an error", code)
	}
	if out.String() != "out" || errBuf.String() != "err" {
		t.Errorf("streams not separated: stdout=%q stderr=%q", out.String(), errBuf.String())
	}
}

func TestDeleteSandbox_TreatsAMissingServiceAsSuccess(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	// A delete that races another delete must not fail a run that already
	// finished, and must not stop a sweep part way through.
	if err := c.deleteSandbox(context.Background(), "svc-1"); err != nil {
		t.Errorf("deleteSandbox on a missing service = %v, want nil", err)
	}
}
