package worker_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// A claim that loses the contest fields runs the contest as a plain eval of
// the first candidate, and nothing downstream notices.
func TestHTTPAPI_Claim_CarriesTheContest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"claim_token":"tok","job":{"id":"job-1","skill_name":"alpha","version":2,
			"contest_id":"contest-1","contest_attempts":3,"candidates":[
			{"skill_id":"s1","skill_name":"alpha","version":2,"label":"alpha@2","reused_from":"contest-0","reused_report":{"skill":"alpha"}},
			{"skill_id":"s2","skill_name":"beta","version":5,"label":"beta@5"}]}}`))
	}))
	defer srv.Close()

	job, _, ok, err := worker.NewHTTPAPI(srv.URL, "test-key").Claim(context.Background(), "w1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("Claim: ok=%v err=%v", ok, err)
	}
	if job.ContestID != "contest-1" || job.ContestAttempts != 3 || len(job.Candidates) != 2 {
		t.Fatalf("contest=%q attempts=%d candidates=%d, want contest-1, 3 and 2",
			job.ContestID, job.ContestAttempts, len(job.Candidates))
	}
	if c := job.Candidates[0]; c.ReusedFrom != "contest-0" || len(c.ReusedReport) == 0 {
		t.Errorf("the reused report was dropped: %+v", c)
	}
}
