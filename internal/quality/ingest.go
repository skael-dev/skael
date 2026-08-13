// Package quality stores and retrieves per-version quality scores.
package quality

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/skael-dev/skael/internal/eval/report"
)

// Record is one scored measurement of a skill version.
type Record struct {
	SkillID string
	Version int

	Headline       float64
	HeadlineCILow  float64 // legacy; new rows carry zeroes
	HeadlineCIHigh float64

	Pillars     json.RawMessage
	PanelMatrix json.RawMessage

	RobustnessGap  *float64 // nil = not computed; zero = floor kept up
	DriftGrade     string
	DriftBreakdown json.RawMessage

	Verified      bool
	PanelComplete bool
	SuiteDerived  bool

	SuiteRef      string
	EngineVersion string
	ModelPanel    json.RawMessage
	Tier          string
	UpliftSource  string
	// JudgeModel is nil for rows written before this field existed. series.go
	// maps nil to a shared "unknown judge" value so those rows still group.
	JudgeModel *string
	JobID      string
	ScoredAt   time.Time

	CriticalForbidViolations int
	ReportJSON               json.RawMessage
}

// FromReport maps a report onto a record. Pure: no database or I/O.
func FromReport(r *report.Report) (Record, error) {
	if r == nil {
		return Record{}, fmt.Errorf("quality.FromReport: report is nil")
	}
	if r.SchemaVersion > report.SchemaVersion {
		return Record{}, fmt.Errorf("quality.FromReport: schema version %d is newer than this binary understands (%d)", r.SchemaVersion, report.SchemaVersion)
	}
	if r.SuiteRef == "" {
		return Record{}, fmt.Errorf("quality.FromReport: report has no suite ref")
	}

	members := r.Members
	if members == nil {
		members = []report.MemberReport{}
	}
	panelMatrix, err := json.Marshal(members)
	if err != nil {
		return Record{}, fmt.Errorf("quality.FromReport: marshal members: %w", err)
	}

	modelPanel := r.ModelPanel
	if modelPanel == nil {
		modelPanel = []report.PanelMember{}
	}
	modelPanelJSON, err := json.Marshal(modelPanel)
	if err != nil {
		return Record{}, fmt.Errorf("quality.FromReport: marshal model panel: %w", err)
	}

	var graderModel *string
	if r.GraderModel != "" {
		graderModel = &r.GraderModel
	}

	// Pillars and DriftBreakdown are legacy columns; written as empty objects
	// so no migration is needed.
	empty := json.RawMessage("{}")

	return Record{
		Headline:       r.Headline,
		Pillars:        empty,
		DriftBreakdown: empty,
		PanelMatrix:    panelMatrix,
		PanelComplete:  r.PanelComplete,
		SuiteRef:       r.SuiteRef,
		EngineVersion:  r.EngineVersion,
		ModelPanel:     modelPanelJSON,
		Tier:           r.Tier,
		JudgeModel:     graderModel,
		ScoredAt:       r.FinishedAt,
	}, nil
}

// FromReportRaw is FromReport plus the raw bytes. The caller passes what it
// received rather than re-marshalling, because a round trip drops unknown fields.
func FromReportRaw(r *report.Report, raw json.RawMessage) (Record, error) {
	rec, err := FromReport(r)
	if err != nil {
		return Record{}, err
	}
	rec.ReportJSON = raw
	return rec, nil
}
