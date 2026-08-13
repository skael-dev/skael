package quality

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/skael-dev/skael/internal/eval/report"
)

// unknownJudge groups all records with no recorded judge. Shared rather than
// unique per row: unique would fragment every pre-migration skill's history
// into one-point series on deploy.
const unknownJudge = "\x00unknown-judge"

// SeriesPoint is one scored version on a trend line.
type SeriesPoint struct {
	Version        int       `json:"version"`
	Headline       float64   `json:"headline_score"`
	HeadlineCILow  float64   `json:"headline_ci_low,omitempty"`
	HeadlineCIHigh float64   `json:"headline_ci_high,omitempty"`
	Verified       bool      `json:"verified"`
	ScoredAt       time.Time `json:"scored_at"`
}

// Series is a run of scores that may be charted together.
type Series struct {
	Key     string        `json:"key"`
	Current bool          `json:"current"`
	Reason  string        `json:"reason"`
	Points  []SeriesPoint `json:"points"`
}

const syntheticSchemaVersion = report.SchemaVersion

// asReport rebuilds the subset of a Report that Comparable reads.
func asReport(skillName string, r Record) *report.Report {
	var panel []report.PanelMember
	if len(r.ModelPanel) > 0 {
		_ = json.Unmarshal(r.ModelPanel, &panel)
	}
	return &report.Report{
		SchemaVersion: syntheticSchemaVersion,
		Skill:         skillName,
		SuiteRef:      r.SuiteRef,
		EngineVersion: r.EngineVersion,
		Tier:          r.Tier,
		ModelPanel:    panel,
		PanelComplete: r.PanelComplete,
		GraderModel:   graderModelFor(r),
	}
}

func graderModelFor(r Record) string {
	if r.JudgeModel != nil {
		return *r.JudgeModel
	}
	return unknownJudge
}

// BuildSeries groups a skill's history into comparable runs. History arrives
// newest-first; the first record's group is the current series.
func BuildSeries(skillName string, history []Record) []Series {
	out := []Series{}
	if len(history) == 0 {
		return out
	}

	var reps []*report.Report

	for _, rec := range history {
		cand := asReport(skillName, rec)
		placed := false
		for i, rep := range reps {
			if ok, _ := rep.Comparable(cand); ok {
				out[i].Points = append(out[i].Points, toPoint(rec))
				placed = true
				break
			}
		}
		if placed {
			continue
		}
		reason := ""
		if len(reps) > 0 {
			_, reason = reps[0].Comparable(cand)
		}
		reps = append(reps, cand)
		out = append(out, Series{
			Key:     fmt.Sprintf("series-%d", len(out)),
			Current: len(out) == 0,
			Reason:  reason,
			Points:  []SeriesPoint{toPoint(rec)},
		})
	}

	for i := range out {
		pts := out[i].Points
		sort.Slice(pts, func(a, b int) bool { return pts[a].Version < pts[b].Version })
	}
	return out
}

func toPoint(r Record) SeriesPoint {
	return SeriesPoint{
		Version:        r.Version,
		Headline:       r.Headline,
		HeadlineCILow:  r.HeadlineCILow,
		HeadlineCIHigh: r.HeadlineCIHigh,
		Verified:       r.Verified,
		ScoredAt:       r.ScoredAt,
	}
}
