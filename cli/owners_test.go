package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/skael-dev/skael/internal/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ownersCall is one HTTP request an owners-test stub server observed.
type ownersCall struct {
	Method string
	Path   string
	Query  string
	Body   string
}

// setupOwnersEnv points SKAEL_URL/SKAEL_KEY at srv for the duration of the
// test, matching the env-var config path LoadConfig checks first.
func setupOwnersEnv(t *testing.T, srv *httptest.Server) {
	t.Helper()
	t.Setenv("SKAEL_URL", srv.URL)
	t.Setenv("SKAEL_KEY", "test-key")
}

// An unknown email must be a hard error naming near matches — never a
// silently-empty rule, which would look like it worked and protect nothing.
func TestOwnersAddUnknownEmailErrorsWithSuggestions(t *testing.T) {
	var calls []ownersCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, ownersCall{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: string(body)})

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/users/search":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"users": []map[string]string{
					{"id": "u-carol", "name": "Carol", "email": "carol@acme.com"},
				},
			})
		default:
			t.Errorf("unexpected request to %s %s — an unresolved email must never reach the rule endpoint", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	setupOwnersEnv(t, srv)

	stderr := captureStderr(t, func() {
		captureStdout(t, func() {
			err := runOwnersAdd(ownersAddCmd, []string{"payments:*", "carl@acme.com"})
			require.NoError(t, err)
		})
	})

	assert.Contains(t, stderr, "carol@acme.com")

	for _, c := range calls {
		if c.Path == "/api/ownership/rules" {
			t.Fatalf("unexpected call to /api/ownership/rules: %+v", c)
		}
	}
}

// `set` replaces the member list wholesale, matching the API.
func TestOwnersSetSendsTheFullMemberList(t *testing.T) {
	var ruleBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/users/search":
			q := r.URL.Query().Get("q")
			var user map[string]string
			switch q {
			case "alice@acme.com":
				user = map[string]string{"id": "u-alice", "name": "Alice", "email": "alice@acme.com"}
			case "bob@acme.com":
				user = map[string]string{"id": "u-bob", "name": "Bob", "email": "bob@acme.com"}
			default:
				t.Fatalf("unexpected search query %q", q)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"users": []map[string]string{user}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/ownership/rules":
			ruleBody = string(body)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "r1", "pattern": "payments:*", "members": []map[string]string{
					{"id": "u-alice", "name": "Alice", "email": "alice@acme.com"},
					{"id": "u-bob", "name": "Bob", "email": "bob@acme.com"},
				},
			})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	setupOwnersEnv(t, srv)

	captureStderr(t, func() {
		captureStdout(t, func() {
			err := runOwnersSet(ownersSetCmd, []string{"payments:*", "alice@acme.com", "bob@acme.com"})
			require.NoError(t, err)
		})
	})

	require.NotEmpty(t, ruleBody)
	var decoded struct {
		Pattern string   `json:"pattern"`
		Members []string `json:"members"`
	}
	require.NoError(t, json.Unmarshal([]byte(ruleBody), &decoded))
	assert.Equal(t, "payments:*", decoded.Pattern)
	assert.ElementsMatch(t, []string{"u-alice", "u-bob"}, decoded.Members)
}

// `add` reads the current list and sends list+1; `rm` sends list-1.
func TestOwnersAddPreservesExistingMembers(t *testing.T) {
	var ruleBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/users/search":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"users": []map[string]string{{"id": "u-bob", "name": "Bob", "email": "bob@acme.com"}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/ownership/rules":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"rules": []map[string]any{
					{"id": "r1", "pattern": "payments:*", "members": []map[string]string{
						{"id": "u-alice", "name": "Alice", "email": "alice@acme.com"},
					}},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/ownership/rules":
			ruleBody = string(body)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "r1", "pattern": "payments:*", "members": []map[string]string{
					{"id": "u-alice", "name": "Alice", "email": "alice@acme.com"},
					{"id": "u-bob", "name": "Bob", "email": "bob@acme.com"},
				},
			})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	setupOwnersEnv(t, srv)

	captureStderr(t, func() {
		captureStdout(t, func() {
			err := runOwnersAdd(ownersAddCmd, []string{"payments:*", "bob@acme.com"})
			require.NoError(t, err)
		})
	})

	require.NotEmpty(t, ruleBody)
	var decoded struct {
		Members []string `json:"members"`
	}
	require.NoError(t, json.Unmarshal([]byte(ruleBody), &decoded))
	assert.ElementsMatch(t, []string{"u-alice", "u-bob"}, decoded.Members)
}

// `show` prints which rule matched — never a mystery why access is what it is.
func TestOwnersShowPrintsTheMatchingRule(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/skills/payments-refunds/owners" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"rule_pattern": "payments:*",
				"owners":       []map[string]string{{"id": "u-alice", "name": "Alice", "email": "alice@acme.com"}},
				"unowned":      false,
			})
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	setupOwnersEnv(t, srv)

	stdout := captureStdout(t, func() {
		captureStderr(t, func() {
			err := runOwnersShow(ownersShowCmd, []string{"payments-refunds"})
			require.NoError(t, err)
		})
	})

	assert.Contains(t, stdout, "payments:*")
}

// JSON mode emits structured output and no styled text.
func TestOwnersListJSONMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/ownership/rules" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"rules": []map[string]any{
					{"id": "r1", "pattern": "payments:*", "members": []map[string]string{
						{"id": "u-alice", "name": "Alice", "email": "alice@acme.com"},
					}},
				},
			})
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	setupOwnersEnv(t, srv)

	ui.JSONMode = true
	defer func() { ui.JSONMode = false }()

	stdout := captureStdout(t, func() {
		captureStderr(t, func() {
			err := runOwnersList(ownersListCmd, nil)
			require.NoError(t, err)
		})
	})

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &decoded), "stdout must be valid JSON: %s", stdout)
	assert.Contains(t, decoded, "rules")
}
