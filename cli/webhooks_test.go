package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A refused address reaches a script as exit 1 and the envelope, not as a
// usage error it would treat as a bug in its own command line.
func TestWebhooksCreateExitsOneOnServer422(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"status":422,"title":"Unprocessable Entity","detail":"refused address: 169.254.169.254"}`))
	}))
	defer server.Close()
	noConfig(t)
	t.Setenv("SKAEL_URL", server.URL)
	t.Setenv("SKAEL_KEY", "k")

	code, stdout := runCLI(t, "webhooks", "create", "--name", "a", "--url", "http://169.254.169.254/", "--event", "set.changed", "--json")
	assert.Equal(t, 1, code)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &envelope), "stdout: %s", stdout)
	assert.Equal(t, "api_error", envelope["code"])
	assert.Contains(t, envelope["error"], "refused address")
}
