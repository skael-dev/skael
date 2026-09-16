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

	// Nil means not measured. Lift is reported only; the gate reads Headline.
	PrimaryScore *float64
	Baseline     *float64
	Lift         *float64

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

	primary, baseline, lift, upliftSource := pairing(r)

	return Record{
		Headline:       r.Headline,
		PrimaryScore:   primary,
		Baseline:       baseline,
		Lift:           lift,
		UpliftSource:   upliftSource,
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

// pairing recomputes the lift rather than reading r.Delta, which holds an
// unpaired number before report schema 3. Baseline has always meant the primary
// member's baseline, so the subtraction is reconstructable for every schema.
func pairing(r *report.Report) (primary, baseline, lift *float64, source string) {
	score, ok := primaryEffectiveness(r)
	if ok {
		primary = &score
	}
	if !r.DeltaMeasured {
		return primary, nil, nil, ""
	}
	b := r.Baseline
	baseline = &b
	source = "fresh"
	if len(r.ReusedBaselines) > 0 {
		source = "reused"
	}
	if ok {
		d := score - b
		lift = &d
	}
	return primary, baseline, lift, source
}

func primaryEffectiveness(r *report.Report) (float64, bool) {
	if len(r.ModelPanel) == 0 {
		return 0, false
	}
	p := r.ModelPanel[0]
	for _, m := range r.Members {
		if m.Member.Agent == p.Agent && m.Member.Model == p.Model && m.Healthy {
			return m.Effectiveness, true
		}
	}
	return 0, false
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
