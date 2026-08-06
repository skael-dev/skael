package quality

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/skill"
)

// RecordOutput is the wire shape for a Record.
type RecordOutput struct {
	SkillID                  string          `json:"skill_id"`
	Version                  int             `json:"version"`
	Headline                 float64         `json:"headline_score"`
	HeadlineCILow            float64         `json:"headline_ci_low,omitempty"`
	HeadlineCIHigh           float64         `json:"headline_ci_high,omitempty"`
	Pillars                  json.RawMessage `json:"pillar_breakdown"`
	PanelMatrix              json.RawMessage `json:"panel_matrix"`
	RobustnessGap            *float64        `json:"robustness_gap,omitempty"`
	DriftGrade               string          `json:"drift_grade,omitempty"`
	DriftBreakdown           json.RawMessage `json:"drift_breakdown"`
	Verified                 bool            `json:"verified"`
	PanelComplete            bool            `json:"panel_complete"`
	SuiteRef                 string          `json:"suite_ref"`
	EngineVersion            string          `json:"engine_version"`
	ModelPanel               json.RawMessage `json:"model_panel"`
	Tier                     string          `json:"tier"`
	UpliftSource             string          `json:"uplift_source,omitempty"`
	JudgeModel               *string         `json:"judge_model,omitempty"`
	JobID                    string          `json:"job_id,omitempty"`
	ScoredAt                 time.Time       `json:"scored_at"`
	CriticalForbidViolations int             `json:"critical_forbid_violations"`
	// ReportJSON is json:"-" deliberately. The summary and history endpoints
	// share this shape and must stay small; the full report is served only by
	// the per-version endpoint, which wraps this struct rather than widening
	// it. Struct tags are ignored by the conversion in toRecordOutput, so the
	// field must still be present here or that conversion stops compiling —
	// which is exactly the drift alarm it exists to be.
	ReportJSON json.RawMessage `json:"-"`
}

// toRecordOutput converts a Record to its wire shape. RecordOutput's fields
// deliberately match Record's, in order and type, so this is a plain
// conversion rather than a field-by-field copy.
func toRecordOutput(rec Record) RecordOutput {
	return RecordOutput(rec)
}

type qualityInput struct {
	Name string `path:"name"`
}

type qualityOutput struct {
	Body RecordOutput
}

type qualityHistoryBody struct {
	History []RecordOutput `json:"history"`
}

type qualityHistoryOutput struct {
	Body qualityHistoryBody
}

// RegisterRoutes wires up the read-only quality endpoints: the skill's most
// recent scored record across all versions, and its full history
// newest-first for the version-over-version trend.
func RegisterRoutes(api huma.API, store *Store, skills *skill.Store) {
	huma.Register(api, huma.Operation{
		OperationID: "get-skill-quality",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}/quality",
		Summary:     "Get the most recent quality score for a skill",
	}, func(ctx context.Context, input *qualityInput) (*qualityOutput, error) {
		sk, err := skills.GetByName(ctx, input.Name)
		if err != nil {
			return nil, huma.Error500InternalServerError("get skill quality: internal error", err)
		}
		if sk == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("skill %q not found", input.Name))
		}

		// Deliberately not pinned to sk.LatestVersion: a skill scored at an
		// earlier version should keep showing that score while a newer,
		// not-yet-scored version is current — pinning to LatestVersion would
		// make the badge vanish on every publish until the next eval lands.
		// The wire shape carries `version`, so a caller can compare it
		// against the skill's own latest_version and render staleness.
		rec, err := store.LatestAcrossVersions(ctx, sk.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("get skill quality: internal error", err)
		}
		if rec == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("skill %q has never been scored", input.Name))
		}

		return &qualityOutput{Body: toRecordOutput(*rec)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-skill-quality-history",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}/quality/history",
		Summary:     "Get the quality score history for a skill, newest first",
	}, func(ctx context.Context, input *qualityInput) (*qualityHistoryOutput, error) {
		sk, err := skills.GetByName(ctx, input.Name)
		if err != nil {
			return nil, huma.Error500InternalServerError("get skill quality history: internal error", err)
		}
		if sk == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("skill %q not found", input.Name))
		}

		hist, err := store.History(ctx, sk.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("get skill quality history: internal error", err)
		}

		out := qualityHistoryBody{History: make([]RecordOutput, 0, len(hist))}
		for _, rec := range hist {
			out.History = append(out.History, toRecordOutput(rec))
		}
		return &qualityHistoryOutput{Body: out}, nil
	})

	type seriesBody struct {
		Series []Series `json:"series"`
	}
	type seriesOutput struct {
		Body seriesBody
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-skill-quality-series",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}/quality/series",
		Summary:     "Get a skill's quality history grouped into comparable series",
	}, func(ctx context.Context, input *qualityInput) (*seriesOutput, error) {
		sk, err := skills.GetByName(ctx, input.Name)
		if err != nil {
			return nil, huma.Error500InternalServerError("get skill quality series: internal error", err)
		}
		if sk == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("skill %q not found", input.Name))
		}
		hist, err := store.History(ctx, sk.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("get skill quality series: internal error", err)
		}
		return &seriesOutput{Body: seriesBody{Series: BuildSeries(input.Name, hist)}}, nil
	})

	type versionInput struct {
		Name    string `path:"name"`
		Version int    `path:"version"`
	}
	// versionOutput embeds RecordOutput so the aggregate fields stay
	// byte-identical to the summary endpoint's, and adds the report the
	// summary deliberately omits. Report is a pointer-free RawMessage that
	// marshals to `null` when absent, which is the signal the detail page
	// branches on to render its aggregates-only view.
	type versionBody struct {
		RecordOutput
		Report json.RawMessage `json:"report"`
	}
	type versionOutput struct {
		Body versionBody
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-skill-quality-version",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}/quality/{version}",
		Summary:     "Get the full quality report for one skill version",
	}, func(ctx context.Context, input *versionInput) (*versionOutput, error) {
		sk, err := skills.GetByName(ctx, input.Name)
		if err != nil {
			return nil, huma.Error500InternalServerError("get skill quality version: internal error", err)
		}
		if sk == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("skill %q not found", input.Name))
		}
		rec, err := store.GetVersion(ctx, sk.ID, input.Version)
		if err != nil {
			return nil, huma.Error500InternalServerError("get skill quality version: internal error", err)
		}
		if rec == nil {
			return nil, huma.Error404NotFound(
				fmt.Sprintf("skill %q version %d has never been scored", input.Name, input.Version))
		}
		body := versionBody{RecordOutput: toRecordOutput(*rec)}
		body.Report = json.RawMessage("null")
		if rec.ReportJSON != nil {
			// The report can contain LLM prose quoting the skill's content
			// (JudgeNote.Evidence). For a released version that content is
			// already public via the download/show endpoints, so the report
			// is unrestricted. For a version still held for review
			// (gate_state != "released"), skill_versions' description/content
			// are json:"-" everywhere else — serving the full report here
			// would leak them indirectly. Only a privileged caller
			// (owner/admin) may see it; everyone else gets the aggregates
			// unchanged with report:null, exactly like the existing
			// "no stored report" case the UI already handles.
			ver, err := skills.GetVersion(ctx, input.Name, input.Version)
			if err != nil {
				return nil, huma.Error500InternalServerError("get skill quality version: internal error", err)
			}
			released := ver != nil && ver.GateState == "released"
			if released || auth.UserFromContext(ctx).IsPrivileged() {
				body.Report = rec.ReportJSON
			}
		}
		return &versionOutput{Body: body}, nil
	})
}
