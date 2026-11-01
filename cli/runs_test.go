package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A script retries the event a batch names, so the 422 must carry its index
// and exit 1, whichever server layer rejected the event.
func TestEventsReportInvalidEvent(t *testing.T) {
	tests := []struct {
		name string
		body string
		want float64
	}{
		{"the batch route", `{"status":422,"title":"Unprocessable Entity","detail":"event 2: role is required","index":2}`, 2},
		{"the request schema", `{"status":422,"title":"Unprocessable Entity","detail":"validation failed","errors":[{"message":"expected date-time","location":"body.events[1].time"}]}`, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var key string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				key = r.Header.Get("Idempotency-Key")
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			noConfig(t)
			t.Setenv("SKAEL_URL", server.URL)
			t.Setenv("SKAEL_KEY", "k")

			file := filepath.Join(t.TempDir(), "events.jsonl")
			require.NoError(t, os.WriteFile(file, []byte("{\"skill\":\"a\"}\n{\"skill\":\"b\"}\n{\"skill\":\"c\"}\n"), 0o600))

			code, stdout := runCLI(t, "events", "report", "--file", file, "--idempotency-key", "k1", "--json")
			assert.Equal(t, 1, code)
			assert.Equal(t, "k1", key)
			var envelope map[string]any
			require.NoError(t, json.Unmarshal([]byte(stdout), &envelope), "stdout: %s", stdout)
			assert.Equal(t, tt.want, envelope["index"])
			assert.Equal(t, "invalid_event", envelope["code"])
		})
	}
}
