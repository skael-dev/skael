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
	type ingestBody struct {
		SkillName     string `json:"skill_name" minLength:"1" maxLength:"255"`
		Agent         string `json:"agent" minLength:"1" maxLength:"128"`
		TriggerType   string `json:"trigger_type" maxLength:"64"`
		ProjectHash   string `json:"project_hash" maxLength:"64"`
		DeveloperHash string `json:"developer_hash" maxLength:"64"`
	}
	type ingestInput struct {
		Body ingestBody
	}
	huma.Register(api, huma.Operation{
		OperationID:   "ingest-event",
		Method:        http.MethodPost,
		Path:          "/api/events",
		Summary:       "Ingest a skill activation event",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, input *ingestInput) (*struct{}, error) {
		if err := store.Insert(ctx, Event{
			SkillName:     input.Body.SkillName,
			Agent:         input.Body.Agent,
			TriggerType:   input.Body.TriggerType,
			ProjectHash:   input.Body.ProjectHash,
			DeveloperHash: input.Body.DeveloperHash,
		}); err != nil {
			return nil, fmt.Errorf("ingest event: %w", err)
		}
		return nil, nil
	})

	// -----------------------------------------------------------------
	// GET /api/analytics/overview?days=30 — KPI strip data
	// -----------------------------------------------------------------
	type overviewInput struct {
		Days int `query:"days" default:"30" minimum:"1" maximum:"365"`
	}
	type overviewOutput struct {
		Body *OverviewData
	}
	huma.Register(api, huma.Operation{
		OperationID: "analytics-overview",
		Method:      http.MethodGet,
		Path:        "/api/analytics/overview",
		Summary:     "Analytics overview for KPI strip",
	}, func(ctx context.Context, input *overviewInput) (*overviewOutput, error) {
		days := input.Days
		if days == 0 {
			days = 30
		}
		data, err := store.GetOverview(ctx, days)
		if err != nil {
			return nil, fmt.Errorf("analytics overview: %w", err)
		}
		return &overviewOutput{Body: data}, nil
	})

	// -----------------------------------------------------------------
	// GET /api/analytics/skills?days=30 — per-skill analytics table
	// -----------------------------------------------------------------
	type skillsAnalyticsInput struct {
		Days int `query:"days" default:"30" minimum:"1" maximum:"365"`
	}
	type skillsAnalyticsOutput struct {
		Body []SkillAnalytics
	}
	huma.Register(api, huma.Operation{
		OperationID: "analytics-skills",
		Method:      http.MethodGet,
		Path:        "/api/analytics/skills",
		Summary:     "Per-skill analytics for table view",
	}, func(ctx context.Context, input *skillsAnalyticsInput) (*skillsAnalyticsOutput, error) {
		days := input.Days
		if days == 0 {
			days = 30
		}
		skills, err := store.GetSkillsAnalytics(ctx, days)
		if err != nil {
			return nil, fmt.Errorf("analytics skills: %w", err)
		}
		return &skillsAnalyticsOutput{Body: skills}, nil
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

	// -----------------------------------------------------------------
	// GET /api/analytics/timeseries?days=30 — daily activation counts
	// -----------------------------------------------------------------
	type timeseriesInput struct {
		Days int `query:"days" default:"30" minimum:"1" maximum:"365"`
	}
	type timeseriesOutput struct {
		Body []DailyCount
	}
	huma.Register(api, huma.Operation{
		OperationID: "analytics-timeseries",
		Method:      http.MethodGet,
		Path:        "/api/analytics/timeseries",
		Summary:     "Daily activation counts for chart",
	}, func(ctx context.Context, input *timeseriesInput) (*timeseriesOutput, error) {
		days := input.Days
		if days == 0 {
			days = 30
		}
		series, err := store.GetTimeSeries(ctx, days)
		if err != nil {
			return nil, fmt.Errorf("analytics timeseries: %w", err)
		}
		return &timeseriesOutput{Body: series}, nil
	})
}
