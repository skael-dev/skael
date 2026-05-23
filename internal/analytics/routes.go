package analytics

import (
	"context"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterRoutes wires up all analytics-related HTTP endpoints onto the
// provided Huma API.
func RegisterRoutes(api huma.API, store *Store) {
	// -----------------------------------------------------------------
	// POST /api/events — ingest a skill activation event
	// -----------------------------------------------------------------
	type ingestInput struct {
		Body Event
	}
	huma.Register(api, huma.Operation{
		OperationID:   "ingest-event",
		Method:        http.MethodPost,
		Path:          "/api/events",
		Summary:       "Ingest a skill activation event",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, input *ingestInput) (*struct{}, error) {
		if err := store.Insert(ctx, input.Body); err != nil {
			return nil, fmt.Errorf("ingest event: %w", err)
		}
		return nil, nil
	})

	// -----------------------------------------------------------------
	// GET /api/skills/{name}/activations?days=30 — activation summary
	// -----------------------------------------------------------------
	type activationsInput struct {
		Name string `path:"name"`
		Days int    `query:"days" default:"30" minimum:"1" maximum:"365"`
	}
	type activationsOutput struct {
		Body *ActivationSummary
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-skill-activations",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}/activations",
		Summary:     "Get activation summary for a skill",
	}, func(ctx context.Context, input *activationsInput) (*activationsOutput, error) {
		days := input.Days
		if days == 0 {
			days = 30
		}
		summary, err := store.GetActivations(ctx, input.Name, days)
		if err != nil {
			return nil, fmt.Errorf("get activations: %w", err)
		}
		return &activationsOutput{Body: summary}, nil
	})
}
