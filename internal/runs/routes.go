package runs

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/skael-dev/skael/internal/auth"
)

const maxBatchEvents = 1000

var eventSkillNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$`)

// ConsumerFromContext returns the account a request reports as. Only a service
// account has a consumer, and no request field can name one.
func ConsumerFromContext(ctx context.Context) (Consumer, error) {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return Consumer{}, huma.Error401Unauthorized("not authenticated")
	}
	if u.Kind != auth.KindService {
		return Consumer{}, huma.Error403Forbidden("only a service account reports runs")
	}
	return Consumer{ID: u.ID, Name: u.Name}, nil
}

// BatchError names the event that made a batch invalid.
type BatchError struct {
	Status int    `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Index  int    `json:"index"`
}

// Error implements error.
func (e *BatchError) Error() string { return e.Detail }

// GetStatus implements huma.StatusError.
func (e *BatchError) GetStatus() int { return e.Status }

func invalidEvent(index int, detail string) error {
	return &BatchError{Status: http.StatusUnprocessableEntity, Title: "Unprocessable Entity", Index: index, Detail: fmt.Sprintf("event %d: %s", index, detail)}
}

// ValidHTTPURL reports whether s is empty or an http or https URL. The UI links
// these values, so another scheme could run script in the app's origin.
func ValidHTTPURL(s string) bool {
	if s == "" {
		return true
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// RunEventBody is one event in a batch. Its fields are optional in the schema
// and checked by hand, so that an invalid event always gets its index.
type RunEventBody struct {
	Skill         string    `json:"skill,omitempty"`
	Version       int       `json:"version,omitempty"`
	Agent         string    `json:"agent,omitempty"`
	Role          string    `json:"role,omitempty"`
	ExternalRunID string    `json:"external_run_id,omitempty"`
	Time          time.Time `json:"time,omitempty"`
	Set           string    `json:"set,omitempty"`
	ResolutionID  string    `json:"resolution_id,omitempty"`
}

// ValidateEvents converts a batch, or names the first invalid event.
func ValidateEvents(in []RunEventBody) ([]Event, error) {
	if len(in) == 0 {
		return nil, huma.Error422UnprocessableEntity("a batch holds at least one event")
	}
	if len(in) > maxBatchEvents {
		return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("a batch holds at most %d events", maxBatchEvents))
	}
	events := make([]Event, len(in))
	for i, e := range in {
		switch {
		case !eventSkillNameRe.MatchString(e.Skill) || len(e.Skill) > 128:
			return nil, invalidEvent(i, fmt.Sprintf("invalid skill %q", e.Skill))
		case e.Version < 1:
			return nil, invalidEvent(i, "version must be 1 or more")
		case e.Agent == "" || len(e.Agent) > 128:
			return nil, invalidEvent(i, "agent is required")
		case e.Role == "" || len(e.Role) > 64:
			return nil, invalidEvent(i, "role is required")
		case e.ExternalRunID == "" || len(e.ExternalRunID) > 255:
			return nil, invalidEvent(i, "external_run_id is required")
		case e.Time.IsZero():
			return nil, invalidEvent(i, "time is required")
		}
		events[i] = Event{
			Skill: e.Skill, Version: e.Version, Agent: e.Agent, Role: e.Role,
			ExternalRunID: e.ExternalRunID, Time: e.Time, Set: e.Set, ResolutionID: e.ResolutionID,
		}
	}
	return events, nil
}

// RunOutcomeBody is the request of POST /api/outcomes and of MCP report_outcome.
type RunOutcomeBody struct {
	ExternalRunID string `json:"external_run_id" minLength:"1" maxLength:"255"`
	Outcome       string `json:"outcome" doc:"succeeded, failed or cancelled"`
	Repairs       int    `json:"repairs,omitempty" minimum:"0"`
	ReviewVerdict string `json:"review_verdict,omitempty" maxLength:"255"`
	EvidenceURL   string `json:"evidence_url,omitempty" maxLength:"2048"`
	Set           string `json:"set,omitempty"`
	ResolutionID  string `json:"resolution_id,omitempty"`
}

// Report validates and stores an outcome for the context's consumer.
func (s *Store) Report(ctx context.Context, body RunOutcomeBody) (*RunRecord, error) {
	c, err := ConsumerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	switch body.Outcome {
	case "succeeded", "failed", "cancelled":
	default:
		return nil, huma.Error422UnprocessableEntity("outcome must be succeeded, failed or cancelled")
	}
	if body.ExternalRunID == "" {
		return nil, huma.Error422UnprocessableEntity("external_run_id is required")
	}
	if body.Repairs < 0 {
		return nil, huma.Error422UnprocessableEntity("repairs cannot be negative")
	}
	if !ValidHTTPURL(body.EvidenceURL) {
		return nil, huma.Error422UnprocessableEntity("evidence_url must be an http or https URL")
	}
	return s.PutOutcome(ctx, c, Outcome{
		ExternalRunID: body.ExternalRunID, Outcome: body.Outcome, Repairs: body.Repairs,
		ReviewVerdict: body.ReviewVerdict, EvidenceURL: body.EvidenceURL, Set: body.Set, ResolutionID: body.ResolutionID,
	})
}

// RegisterRoutes wires the run endpoints.
func RegisterRoutes(api huma.API, store *Store) {
	type runBatchBody struct {
		Events []RunEventBody `json:"events"`
	}
	type runBatchOutputBody struct {
		BatchResult
	}
	huma.Register(api, huma.Operation{
		OperationID: "ingest-event-batch", Method: http.MethodPost, Path: "/api/events/batch",
		Summary: "Report the activations of automated runs",
	}, func(ctx context.Context, in *struct {
		IdempotencyKey string `header:"Idempotency-Key" maxLength:"255"`
		Body           runBatchBody
	}) (*struct{ Body runBatchOutputBody }, error) {
		c, err := ConsumerFromContext(ctx)
		if err != nil {
			return nil, err
		}
		events, err := ValidateEvents(in.Body.Events)
		if err != nil {
			return nil, err
		}
		res, err := store.InsertBatch(ctx, c, in.IdempotencyKey, events)
		if err != nil {
			return nil, err
		}
		return &struct{ Body runBatchOutputBody }{Body: runBatchOutputBody{BatchResult: *res}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "report-outcome", Method: http.MethodPost, Path: "/api/outcomes",
		Summary: "Report how an automated run ended",
	}, func(ctx context.Context, in *struct{ Body RunOutcomeBody }) (*struct{ Body RunRecord }, error) {
		rec, err := store.Report(ctx, in.Body)
		if err != nil {
			return nil, err
		}
		return &struct{ Body RunRecord }{Body: *rec}, nil
	})

	type skillOutcomesBody struct {
		MinComparableRuns int                `json:"min_comparable_runs"`
		Consumers         []ConsumerOutcomes `json:"consumers"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-skill-outcomes", Method: http.MethodGet, Path: "/api/skills/{name}/outcomes",
		Summary: "Outcomes of the runs that loaded each version of a skill",
	}, func(ctx context.Context, in *struct {
		Name string `path:"name"`
	}) (*struct{ Body skillOutcomesBody }, error) {
		consumers, err := store.SkillOutcomes(ctx, in.Name)
		if err != nil {
			return nil, err
		}
		return &struct{ Body skillOutcomesBody }{Body: skillOutcomesBody{MinComparableRuns: MinComparableRuns, Consumers: consumers}}, nil
	})
}
