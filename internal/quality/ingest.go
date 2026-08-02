// Package quality maps an eval report onto a stored quality record and
// persists it, so a score can be read back per skill version and compared
// across versions.
package quality

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/spec"
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

	modelPanel := r.ModelPanel
	if modelPanel == nil {
		modelPanel = []report.PanelMember{}
	}
	modelPanelJSON, err := json.Marshal(modelPanel)
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

	var judgeModel *string
	if r.JudgeModel != "" {
		judgeModel = &r.JudgeModel
	}

	criticalViolations := 0
	for _, task := range r.Tasks {
		for _, rd := range task.Drift {
			for _, v := range rd.Violations {
				if v.Severity == spec.SeverityCritical {
					// Count violation records, not Hits: three hits of one
					// forbid rule in one run is one violated rule, and
					// summing hits would make a chatty rule look like a
					// worse breach than a quiet one.
					criticalViolations++
				}
			}
		}
	}

	return Record{
		Headline:                 r.Headline,
		HeadlineCILow:            r.HeadlineCI[0],
		HeadlineCIHigh:           r.HeadlineCI[1],
		Pillars:                  pillarsJSON,
		PanelMatrix:              panelMatrix,
		RobustnessGap:            r.RobustnessGap,
		DriftGrade:               driftGrade,
		DriftBreakdown:           driftJSON,
		PanelComplete:            r.PanelComplete,
		SuiteRef:                 r.SuiteRef,
		EngineVersion:            r.EngineVersion,
		ModelPanel:               modelPanelJSON,
		Tier:                     r.Tier,
		UpliftSource:             string(r.UpliftSource),
		JudgeModel:               judgeModel,
		ScoredAt:                 r.FinishedAt,
		CriticalForbidViolations: criticalViolations,
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

// memberKey identifies a panel member within a report's per-member maps.
func memberKey(m report.PanelMember) string {
	return m.Agent + "/" + m.Model
}
