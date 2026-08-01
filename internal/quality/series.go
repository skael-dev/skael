package quality

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/score"
)

// SeriesPoint is one scored version on a trend line.
type SeriesPoint struct {
	Version        int       `json:"version"`
	Headline       float64   `json:"headline_score"`
	HeadlineCILow  float64   `json:"headline_ci_low"`
	HeadlineCIHigh float64   `json:"headline_ci_high"`
	Verified       bool      `json:"verified"`
	ScoredAt       time.Time `json:"scored_at"`
}

// Series is a run of scores that may honestly be charted together.
type Series struct {
	// Key is opaque and stable within one response; the client uses it for
	// React keys and nothing else.
	Key     string        `json:"key"`
	Current bool          `json:"current"`
	// Reason is empty on the current series and otherwise names, in the
	// engine's own words, why this run cannot be charted with it.
	Reason string        `json:"reason"`
	Points []SeriesPoint `json:"points"`
}

// syntheticSchemaVersion is the same for every reconstructed report on
// purpose. skill_quality does not store the report's schema version, so this
// dimension cannot differentiate stored records; EngineVersion covers the
// same ground (a scoring-logic change moves it) and is stored.
const syntheticSchemaVersion = report.SchemaVersion

// asReport rebuilds the subset of a Report that Comparable actually reads.
// Reconstructing rather than reimplementing means the comparability rule has
// exactly one definition, in the engine, where it belongs. Skill is identical
// by construction here since all records come from one skill.
func asReport(skillName string, r Record) *report.Report {
	var panel []report.PanelMember
	if len(r.ModelPanel) > 0 {
		// A record whose panel will not decode is compared on its other
		// fields with an empty panel: it groups with other undecodable
		// records rather than crashing the whole trend.
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
		UpliftSource:  score.UpliftSource(r.UpliftSource),
	}
}

// BuildSeries groups a skill's history into runs that report.Comparable
// considers chartable together. history arrives newest-first (Store.History's
// order), so the first record's group is the current one.
//
// Every record appears in exactly one series and nothing is dropped: an
// operator who re-runs against a new panel must be able to see that the score
// moved because the panel changed, which requires the older run to stay
// visible with the reason attached.
func BuildSeries(skillName string, history []Record) []Series {
	out := []Series{}
	if len(history) == 0 {
		return out
	}

	// reps[i] is the representative report of out[i] — the first record
	// assigned to that series, which every later candidate is compared to.
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
			// Always explain relative to the current series, which is the
			// one the reader is looking at.
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

	// Within a series, ascend by version so a sparkline reads left to right
	// in time. Across series, the current one stays first.
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
