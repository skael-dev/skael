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
		Days   int    `query:"days"   default:"30" minimum:"1" maximum:"365"`
		Limit  int    `query:"limit"  default:"50" minimum:"1" maximum:"100"`
		Offset int    `query:"offset" default:"0"  minimum:"0"`
		Sort   string `query:"sort"   default:"activations"`
		Q      string `query:"q"`
		Tag    string `query:"tag"`
	}
	type skillsAnalyticsOutput struct {
		Body struct {
			Skills []SkillAnalytics `json:"skills"`
			Total  int              `json:"total"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "analytics-skills",
		Method:      http.MethodGet,
		Path:        "/api/analytics/skills",
		Summary:     "Per-skill analytics for table view (paginated)",
	}, func(ctx context.Context, input *skillsAnalyticsInput) (*skillsAnalyticsOutput, error) {
		days := input.Days
		if days == 0 {
			days = 30
		}
		skills, total, err := store.GetSkillsAnalytics(ctx, days, SkillsQuery{
			Limit: input.Limit, Offset: input.Offset, Sort: input.Sort, Query: input.Q, Tag: input.Tag,
		})
		if err != nil {
			return nil, fmt.Errorf("analytics skills: %w", err)
		}
		out := &skillsAnalyticsOutput{}
		out.Body.Skills = skills
		out.Body.Total = total
		return out, nil
	})

	type skillsTagsOutput struct {
		Body struct {
			Tags []string `json:"tags"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "skills-tags",
		Method:      http.MethodGet,
		Path:        "/api/skills/tags",
		Summary:     "Distinct tags across all skills",
	}, func(ctx context.Context, _ *struct{}) (*skillsTagsOutput, error) {
		tags, err := store.GetAllTags(ctx)
		if err != nil {
			return nil, fmt.Errorf("skills tags: %w", err)
		}
		out := &skillsTagsOutput{}
		out.Body.Tags = tags
		return out, nil
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
	// GET /api/skills/{name}/timeseries?days=30 — per-agent daily counts
	// -----------------------------------------------------------------
	type skillTimeseriesInput struct {
		Name string `path:"name"`
		Days int    `query:"days" default:"30" minimum:"1" maximum:"365"`
	}
	type skillTimeseriesOutput struct {
		Body []AgentDailyCount
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-skill-timeseries",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}/timeseries",
		Summary:     "Get per-agent daily activation counts for a skill",
	}, func(ctx context.Context, input *skillTimeseriesInput) (*skillTimeseriesOutput, error) {
		days := input.Days
		if days == 0 {
			days = 30
		}
		series, err := store.GetSkillTimeSeries(ctx, input.Name, days)
		if err != nil {
			return nil, fmt.Errorf("get skill timeseries: %w", err)
		}
		return &skillTimeseriesOutput{Body: series}, nil
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

	// -----------------------------------------------------------------
	// GET /api/analytics/unregistered?days=30 — unregistered skills
	// -----------------------------------------------------------------
	type unregisteredInput struct {
		Days int `query:"days" default:"30" minimum:"1" maximum:"365"`
	}
	type unregisteredOutput struct {
		Body []UnregisteredSkill
	}
	huma.Register(api, huma.Operation{
		OperationID: "analytics-unregistered",
		Method:      http.MethodGet,
		Path:        "/api/analytics/unregistered",
		Summary:     "List unregistered skills with activation data",
	}, func(ctx context.Context, input *unregisteredInput) (*unregisteredOutput, error) {
		days := input.Days
		if days == 0 {
			days = 30
		}
		skills, err := store.GetUnregisteredSkills(ctx, days)
		if err != nil {
			return nil, fmt.Errorf("analytics unregistered: %w", err)
		}
		return &unregisteredOutput{Body: skills}, nil
	})

	// -----------------------------------------------------------------
	// POST /api/analytics/dismiss — dismiss an unregistered skill
	// -----------------------------------------------------------------
	type dismissBody struct {
		Name string `json:"name" minLength:"1"`
	}
	type dismissInput struct {
		Body dismissBody
	}
	huma.Register(api, huma.Operation{
		OperationID:   "dismiss-skill",
		Method:        http.MethodPost,
		Path:          "/api/analytics/dismiss",
		Summary:       "Dismiss an unregistered skill",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, input *dismissInput) (*struct{}, error) {
		if err := store.DismissSkill(ctx, input.Body.Name); err != nil {
			return nil, fmt.Errorf("dismiss skill: %w", err)
		}
		return nil, nil
	})
}
