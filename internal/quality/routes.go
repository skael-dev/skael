package quality

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/skael-dev/skael/internal/skill"
)

// recordOutput is the wire shape for a Record.
type recordOutput struct {
	SkillID        string          `json:"skill_id"`
	Version        int             `json:"version"`
	Headline       float64         `json:"headline_score"`
	HeadlineCILow  float64         `json:"headline_ci_low"`
	HeadlineCIHigh float64         `json:"headline_ci_high"`
	Pillars        json.RawMessage `json:"pillar_breakdown"`
	PanelMatrix    json.RawMessage `json:"panel_matrix"`
	RobustnessGap  *float64        `json:"robustness_gap,omitempty"`
	DriftGrade     string          `json:"drift_grade,omitempty"`
	DriftBreakdown json.RawMessage `json:"drift_breakdown"`
	Verified       bool            `json:"verified"`
	PanelComplete  bool            `json:"panel_complete"`
	SuiteRef       string          `json:"suite_ref"`
	EngineVersion  string          `json:"engine_version"`
	ModelPanel     json.RawMessage `json:"model_panel"`
	Tier           string          `json:"tier"`
	UpliftSource   string          `json:"uplift_source,omitempty"`
	JobID          string          `json:"job_id,omitempty"`
	ScoredAt       time.Time       `json:"scored_at"`
}

// toRecordOutput converts a Record to its wire shape. recordOutput's fields
// deliberately match Record's, in order and type, so this is a plain
// conversion rather than a field-by-field copy.
func toRecordOutput(rec Record) recordOutput {
	return recordOutput(rec)
}

type qualityInput struct {
	Name string `path:"name"`
}

type qualityOutput struct {
	Body recordOutput
}

type qualityHistoryBody struct {
	History []recordOutput `json:"history"`
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

		out := qualityHistoryBody{History: make([]recordOutput, 0, len(hist))}
		for _, rec := range hist {
			out.History = append(out.History, toRecordOutput(rec))
		}
		return &qualityHistoryOutput{Body: out}, nil
	})
}
