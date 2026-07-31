// Package quality maps an eval report onto a stored quality record and
// persists it, so a score can be read back per skill version and compared
// across versions.
package quality

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/skael-dev/skael/internal/eval/report"
)

// Record is one scored measurement of a skill version, as stored in
// skill_quality.
type Record struct {
	SkillID string
	Version int

	Headline       float64
	HeadlineCILow  float64
	HeadlineCIHigh float64

	Pillars     json.RawMessage
	PanelMatrix json.RawMessage

	// RobustnessGap is nil when it could not be computed — never a zero. A
	// zero means the floor model kept up with the strong one; nil means the
	// comparison was not defined for this run.
	RobustnessGap  *float64
	DriftGrade     string
	DriftBreakdown json.RawMessage

	Verified      bool
	PanelComplete bool

	SuiteRef      string
	EngineVersion string
	ModelPanel    json.RawMessage
	Tier          string
	JobID         string
	ScoredAt      time.Time
}

// FromReport maps a report onto a record. It is pure: no database, no clock,
// no I/O — that is what makes it testable, and what lets a later ingestion
// endpoint call it before deciding whether to write anything.
func FromReport(r *report.Report) (Record, error) {
	if r.SchemaVersion > report.SchemaVersion {
		return Record{}, fmt.Errorf("quality.FromReport: schema version %d is newer than this binary understands (%d)", r.SchemaVersion, report.SchemaVersion)
	}
	if r.SuiteRef == "" {
		return Record{}, fmt.Errorf("quality.FromReport: report has no suite ref")
	}

	panelMatrix, err := json.Marshal(r.Members)
	if err != nil {
		return Record{}, fmt.Errorf("quality.FromReport: marshal members: %w", err)
	}

	// Pillars is the per-member breakdown, keyed the same way as
	// panel_matrix: only healthy members contribute, since an unhealthy
	// member's pillars are not a measurement.
	pillars := make(map[string]interface{}, len(r.Members))
	for _, m := range r.Members {
		if !m.Healthy {
			continue
		}
		pillars[memberKey(m.Member)] = m.Pillars
	}
	pillarsJSON, err := json.Marshal(pillars)
	if err != nil {
		return Record{}, fmt.Errorf("quality.FromReport: marshal pillars: %w", err)
	}

	driftBreakdown := make(map[string]interface{}, len(r.Members))
	for _, m := range r.Members {
		if !m.Healthy {
			continue
		}
		driftBreakdown[memberKey(m.Member)] = m.Drift
	}
	driftJSON, err := json.Marshal(driftBreakdown)
	if err != nil {
		return Record{}, fmt.Errorf("quality.FromReport: marshal drift breakdown: %w", err)
	}

	modelPanelJSON, err := json.Marshal(r.ModelPanel)
	if err != nil {
		return Record{}, fmt.Errorf("quality.FromReport: marshal model panel: %w", err)
	}

	// DriftGrade comes from the first healthy member, and "" when there is
	// none — an absent grade, not a guess.
	driftGrade := ""
	for _, m := range r.Members {
		if m.Healthy {
			driftGrade = m.DriftGrade
			break
		}
	}

	return Record{
		Headline:       r.Headline,
		HeadlineCILow:  r.HeadlineCI[0],
		HeadlineCIHigh: r.HeadlineCI[1],
		Pillars:        pillarsJSON,
		PanelMatrix:    panelMatrix,
		RobustnessGap:  r.RobustnessGap,
		DriftGrade:     driftGrade,
		DriftBreakdown: driftJSON,
		PanelComplete:  r.PanelComplete,
		SuiteRef:       r.SuiteRef,
		EngineVersion:  r.EngineVersion,
		ModelPanel:     modelPanelJSON,
		Tier:           r.Tier,
		ScoredAt:       r.FinishedAt,
	}, nil
}

// memberKey identifies a panel member within a report's per-member maps.
func memberKey(m report.PanelMember) string {
	return m.Agent + "/" + m.Model
}
