package worker_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/worker"
)

// A stolen lease can surface as either 409 (the server can tell the lease
// lapsed) or 403 (the claim just doesn't verify — which also covers the
// live-lease-but-reclaimed race; see internal/evalqueue/routes.go's
// heartbeat handler). Heartbeat must treat both as ErrLeaseLost so the
// worker abandons promptly regardless of which one the server returns.
func TestHTTPAPI_Heartbeat_Treats403AndConflictAsLeaseLost(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer srv.Close()

			api := worker.NewHTTPAPI(srv.URL, "test-key")
			err := api.Heartbeat(context.Background(), evalqueue.JobID("job-1"), "tok")
			if !errors.Is(err, evalqueue.ErrLeaseLost) {
				t.Fatalf("Heartbeat on a %d response = %v, want evalqueue.ErrLeaseLost", status, err)
			}
		})
	}
}

// A heartbeat failure unrelated to claim validity (a 500, a timeout) must
// not be reported as a lease loss — that would abandon a run the worker
// still legitimately owns.
func TestHTTPAPI_Heartbeat_DoesNotTreatAServerErrorAsLeaseLost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	api := worker.NewHTTPAPI(srv.URL, "test-key")
	err := api.Heartbeat(context.Background(), evalqueue.JobID("job-1"), "tok")
	if err == nil {
		t.Fatal("Heartbeat on a 500 response returned nil")
	}
	if errors.Is(err, evalqueue.ErrLeaseLost) {
		t.Fatal("Heartbeat treated a 500 as a lease loss")
	}
}
