package quality_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/quality"
)

func rec(version int, headline float64, suiteRef, engine, tier string, panel []report.PanelMember, complete bool) quality.Record {
	panelJSON, _ := json.Marshal(panel)
	return quality.Record{
		Version: version, Headline: headline, SuiteRef: suiteRef,
		EngineVersion: engine, Tier: tier, ModelPanel: panelJSON,
		PanelComplete: complete, UpliftSource: "control",
		ScoredAt: time.Date(2026, 8, 1, 0, version, 0, 0, time.UTC),
	}
}

var panelA = []report.PanelMember{{Agent: "claude-code", Model: "strong", Class: "strong"}}
var panelB = []report.PanelMember{{Agent: "claude-code", Model: "floor", Class: "floor"}}

// The whole point: same suite and panel across versions is one series, so
// "did v4 beat v3?" is answerable.
func TestBuildSeries_SameSuiteAndPanelIsOneSeries(t *testing.T) {
	got := quality.BuildSeries("s", []quality.Record{
		rec(4, 78, "sha-1", "v1.0.0", "full", panelA, true),
		rec(3, 74, "sha-1", "v1.0.0", "full", panelA, true),
	})
	if len(got) != 1 {
		t.Fatalf("series count = %d, want 1", len(got))
	}
	if !got[0].Current {
		t.Fatal("the only series must be the current one")
	}
	if len(got[0].Points) != 2 {
		t.Fatalf("points = %d, want 2", len(got[0].Points))
	}
	// Points ascend by version so a sparkline reads left-to-right in time.
	if got[0].Points[0].Version != 3 || got[0].Points[1].Version != 4 {
		t.Fatalf("points out of order: %+v", got[0].Points)
	}
}

func TestBuildSeries_DifferentPanelSplitsAndNamesTheReason(t *testing.T) {
	got := quality.BuildSeries("s", []quality.Record{
		rec(4, 78, "sha-1", "v1.0.0", "full", panelB, true), // newest
		rec(3, 74, "sha-1", "v1.0.0", "full", panelA, true),
	})
	if len(got) != 2 {
		t.Fatalf("series count = %d, want 2", len(got))
	}
	if !got[0].Current {
		t.Fatal("the newest record's series must be first and current")
	}
	if got[1].Current {
		t.Fatal("only one series may be current")
	}
	if got[1].Reason == "" {
		t.Fatal("a non-current series must name why it is not comparable")
	}
	if !strings.Contains(got[1].Reason, "panel") {
		t.Fatalf("reason = %q, want it to name the model panel", got[1].Reason)
	}
}

func TestBuildSeries_DifferentSuiteSplits(t *testing.T) {
	got := quality.BuildSeries("s", []quality.Record{
		rec(4, 78, "sha-2", "v1.0.0", "full", panelA, true),
		rec(3, 74, "sha-1", "v1.0.0", "full", panelA, true),
	})
	if len(got) != 2 {
		t.Fatalf("series count = %d, want 2", len(got))
	}
	if !strings.Contains(got[1].Reason, "suite") {
		t.Fatalf("reason = %q, want it to name the suite", got[1].Reason)
	}
}

func TestBuildSeries_IncompletePanelIsNeverChartedWithComplete(t *testing.T) {
	got := quality.BuildSeries("s", []quality.Record{
		rec(4, 78, "sha-1", "v1.0.0", "full", panelA, true),
		rec(3, 20, "sha-1", "v1.0.0", "full", panelA, false),
	})
	if len(got) != 2 {
		t.Fatalf("series count = %d, want 2 — an incomplete panel's minimum is over fewer members", len(got))
	}
}

func TestBuildSeries_EmptyHistoryIsEmptySlice(t *testing.T) {
	got := quality.BuildSeries("s", nil)
	if got == nil {
		t.Fatal("want an empty slice, not nil: it marshals to [] not null")
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}
