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

	Headline float64
	// No longer populated — the report's confidence interval was removed. Kept
	// so historical rows still decode and no migration is needed; new rows
	// carry zeroes and the API omits them.
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
	// SuiteDerived is set from the job row, not the report body — see
	// FromReportRaw for why Verified and Version follow the same rule.
	SuiteDerived bool

	SuiteRef      string
	EngineVersion string
	ModelPanel    json.RawMessage
	Tier          string
	// UpliftSource is one of the facts report.Comparable checks before
	// treating two reports' scores as a fair comparison — preserved here
	// alongside the other Comparable fields (SuiteRef, EngineVersion, Tier,
	// ModelPanel, PanelComplete) so a version-over-version trend can tell
	// whether its own comparison is valid without re-fetching every report.
	UpliftSource string
	// JudgeModel mirrors report.Report.JudgeModel — preserved alongside the
	// other Comparable fields so BuildSeries can reconstruct a report that
	// groups on it. nil for a row written before this field existed, and for
	// any run where the caller building the report could not determine which
	// model judged it. asReport (internal/quality/series.go) maps nil to one
	// shared "unknown judge" value, distinct from every real model name but
	// shared across every unknown row — see its doc for why records with no
	// recorded judge still group with each other rather than fragmenting.
	JudgeModel *string
	JobID      string
	ScoredAt   time.Time

	// CriticalForbidViolations counts critical-severity forbid-rule
	// violations observed across every run in the report. It is the one
	// signal in which the evaluation is evidence against the skill rather
	// than for it: a version whose runs violated its own contract does not
	// clear a held publish however high its headline score.
	//
	// Zero here means none were observed, which is a real measurement. The
	// gate expresses "not measured at all" as a nil *QualityState, never as
	// a zero.
	CriticalForbidViolations int

	// ReportJSON is the worker's full report exactly as posted, kept so the
	// detail page can show judge evidence and per-task contract violations —
	// facts the aggregates above deliberately compress away. Nil for every
	// row written before migration 015, and for any record read by a query
	// that does not select it: only Store.GetVersion does, because the
	// payload is large and no list or summary needs it.
	ReportJSON json.RawMessage
}

// FromReport maps a report onto a record. It is pure: no database, no clock,
// no I/O — that is what makes it testable, and what lets a later ingestion
// endpoint call it before deciding whether to write anything.
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

	// Baseline, Delta and TriggerF1 are deliberately not columns yet. They
	// live in ReportJSON, which the detail page already reads. Adding columns
	// is a schema change, and this pass does not make one.
	//
	// Pillars and DriftBreakdown are the reverse: columns whose contents no
	// longer exist. They are written as empty objects rather than dropped, so
	// no migration is needed and no historical row changes meaning.
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

// FromReportRaw is FromReport plus the report's own serialised bytes. The
// caller passes what it received rather than re-marshalling r: a round trip
// through the struct would silently drop any field this binary does not know,
// and the stored report is meant to outlive this binary's schema.
func FromReportRaw(r *report.Report, raw json.RawMessage) (Record, error) {
	rec, err := FromReport(r)
	if err != nil {
		return Record{}, err
	}
	rec.ReportJSON = raw
	return rec, nil
}
