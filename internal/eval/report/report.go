// Package report defines the machine-readable result of an eval run: the
// schema a CI job reads, the worker posts, and the UI charts.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// SchemaVersion is the report schema. Load refuses a newer schema.
// Version 2 is the expectation pass rate; version 1 was a geometric mean.
// Version 3 redefines Delta. See Delta.
const SchemaVersion = 3

// PanelMember identifies one model-panel entry on the report.
type PanelMember struct {
	Agent      string `json:"agent"`
	Model      string `json:"model"`
	Class      string `json:"class"`
	CLIVersion string `json:"cli_version"`
	// Provider names the backend that served the member: empty for
	// Anthropic's own API, otherwise the provider type and the endpoint's
	// host, such as "openrouter" or "openai-compatible:r.openai.azure.com".
	// A changed provider splits the trend like a changed model.
	Provider string `json:"provider,omitempty"`
}

// MemberReport is one panel member's measured result.
type MemberReport struct {
	Member PanelMember `json:"member"`
	// Effectiveness is this member's 0–100 score. The name predates the
	// pipeline change; renaming it would break stored decoders.
	Effectiveness float64 `json:"effectiveness"`
	// Healthy is false when the member's probe failed.
	Healthy bool   `json:"healthy"`
	Detail  string `json:"detail,omitempty"`
	// MetaPartial is true when session metadata was rebuilt from store columns
	// rather than the run's artifact.
	MetaPartial       bool   `json:"meta_partial,omitempty"`
	MetaPartialReason string `json:"meta_partial_reason,omitempty"`
}

// ConditionReport tallies expectations for one eval/condition/model.
type ConditionReport struct {
	Condition store.Condition `json:"condition"`
	Model     string          `json:"model"`
	Passes    int             `json:"passes"`
	Runs      int             `json:"runs"`
}

// GradeNote is one graded session's expectations and evidence.
type GradeNote struct {
	Model        string              `json:"model"`
	Condition    store.Condition     `json:"condition"`
	Attempt      int                 `json:"attempt"`
	Expectations []score.Expectation `json:"expectations,omitempty"`
}

// TaskReport is one non-void eval's results.
type TaskReport struct {
	TaskID     string            `json:"task_id"`
	Conditions []ConditionReport `json:"conditions,omitempty"`
	Grades     []GradeNote       `json:"grades,omitempty"`
	// Fired lists, per session, the skills that fired. Empty when no session
	// fired any skill, or when the sessions were resumed without their events.
	Fired []FiredNote `json:"fired,omitempty"`
}

// FiredNote is the skills that fired in one session of a task.
type FiredNote struct {
	Model     string          `json:"model"`
	Condition store.Condition `json:"condition"`
	Attempt   int             `json:"attempt"`
	Skills    []string        `json:"skills"`
}

// VoidTask is an eval excluded from scoring, listed so the reader can see it.
type VoidTask struct {
	TaskID string `json:"task_id"`
	Reason string `json:"reason"`
}

// DroppedGrade is a session that ran but whose grade call failed after every
// retry. It is dropped from the denominator rather than scored as a failure,
// and listed here so the reader can see what the score did not include.
type DroppedGrade struct {
	TaskID    string `json:"task_id"`
	Condition string `json:"condition"`
	Attempt   int    `json:"attempt"`
	Reason    string `json:"reason"`
}

// Report is the machine-readable result of one eval run against one skill.
type Report struct {
	SchemaVersion int    `json:"schema_version"`
	Skill         string `json:"skill"`
	SpecVersion   int    `json:"spec_version"`
	Tier          string `json:"tier"`
	SuiteRef      string `json:"suite_ref"`
	EngineVersion string `json:"engine_version"`

	ModelPanel    []PanelMember `json:"model_panel"`
	PanelComplete bool          `json:"panel_complete"`
	// SandboxImage identifies the environment the panel ran in. Empty on a
	// report written before it was recorded.
	SandboxImage string `json:"sandbox_image,omitempty"`

	// Headline is the published 0–100 score: minimum across healthy members.
	Headline float64 `json:"headline"`
	// Baseline is the no-skill measurement, taken on the primary member.
	Baseline float64 `json:"baseline"`
	// Delta is the lift: the primary member's own effectiveness minus that same
	// member's baseline. Before schema 3 it subtracted that baseline from the
	// whole-panel Headline minimum, so a deep-tier run could report a negative
	// lift for a skill that helped. schema_version says which one a stored
	// report carries.
	Delta float64 `json:"delta"`
	// False when no baseline ran, or when the primary member did not score.
	DeltaMeasured bool `json:"delta_measured"`
	// BaselineWipeout is true when the baseline passed no expectation at all.
	BaselineWipeout bool `json:"baseline_wipeout,omitempty"`

	// TokensMedian is the median token spend with the skill installed;
	// TokensMedianBaseline is the same without it.
	TokensMedian         int64 `json:"tokens_median,omitempty"`
	TokensMedianBaseline int64 `json:"tokens_median_baseline,omitempty"`

	// TriggerF1 is the trigger smoke check. Reported beside the headline, not
	// folded into it.
	TriggerF1 float64 `json:"trigger_f1"`

	Members []MemberReport `json:"members"`

	Tasks     []TaskReport `json:"tasks"`
	VoidTasks []VoidTask   `json:"void_tasks,omitempty"`

	DroppedGrades []DroppedGrade `json:"dropped_grades,omitempty"`

	// ReusedBaselines lists the task ids whose baseline session came from an
	// earlier eval rather than a fresh run.
	ReusedBaselines []string `json:"reused_baselines,omitempty"`

	TriggerInferred bool `json:"trigger_inferred"`
	// TriggerSource is the panel member the trigger probes ran on.
	TriggerSource PanelMember `json:"trigger_source,omitempty"`
	// TriggerUnknown is the count of probes excluded from the confusion matrix
	// because their session could not be measured.
	TriggerUnknown int `json:"trigger_unknown,omitempty"`

	// GraderModel identifies which model graded this run.
	GraderModel string `json:"grader_model,omitempty"`

	// Set is the measurement with a set installed beside the skill, or nil
	// when no set was. It never changes the headline, the lift or the gate.
	Set *SetReport `json:"set,omitempty"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// Save writes r as indented JSON.
func (r *Report) Save(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// Load reads a Report. Rejects a newer schema version.
func Load(r io.Reader) (*Report, error) {
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("report.Load: %w", err)
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return nil, fmt.Errorf("report.Load: %w", err)
	}
	if probe.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf("report.Load: schema version %d is newer than this binary understands (%d)", probe.SchemaVersion, SchemaVersion)
	}
	var rep Report
	if err := json.Unmarshal(b, &rep); err != nil {
		return nil, fmt.Errorf("report.Load: %w", err)
	}
	return &rep, nil
}

// Comparable reports whether two reports measure the same thing. A changed
// suite, panel, tier, grader, or engine version makes scores incomparable.
func (r *Report) Comparable(o *Report) (bool, string) {
	if r != nil && o != nil && r.Skill != o.Skill {
		return false, fmt.Sprintf("different skills (%s and %s)", r.Skill, o.Skill)
	}
	return r.SameConditions(o)
}

// SameConditions reports whether two reports ran under the same conditions,
// whatever skill each measured. A contest reusing one candidate's report from
// another contest needs this and not Comparable, because its candidates may
// be different skills.
func (r *Report) SameConditions(o *Report) (bool, string) {
	switch {
	case r == nil || o == nil:
		return false, "one of the reports is absent"
	case r.SchemaVersion != o.SchemaVersion:
		return false, fmt.Sprintf("different report schemas (%d and %d)", r.SchemaVersion, o.SchemaVersion)
	case r.SuiteRef != o.SuiteRef:
		return false, fmt.Sprintf("different eval sets (%s and %s): the evals are not the same", suite.ShortRef(r.SuiteRef), suite.ShortRef(o.SuiteRef))
	case r.EngineVersion != o.EngineVersion:
		return false, fmt.Sprintf("different engine versions (%s and %s): a scoring-logic change can move the number without the skill changing", r.EngineVersion, o.EngineVersion)
	case r.Tier != o.Tier:
		return false, fmt.Sprintf("different tiers (%s and %s): a smoke score and a full score are not the same measurement", r.Tier, o.Tier)
	case !samePanel(r.ModelPanel, o.ModelPanel):
		return false, "different model panels: a score change could be the models rather than the skill"
	case !sameCLIVersions(r.ModelPanel, o.ModelPanel):
		return false, "different agent CLI versions on the panel: a score change could be the CLI rather than the skill"
	case r.SandboxImage != "" && o.SandboxImage != "" && r.SandboxImage != o.SandboxImage:
		return false, fmt.Sprintf("different sandbox images (%s and %s): the environment can move the number without the skill changing", r.SandboxImage, o.SandboxImage)
	case r.PanelComplete != o.PanelComplete:
		return false, "one panel was incomplete, so its minimum was taken over fewer members"
	case r.GraderModel != o.GraderModel:
		return false, fmt.Sprintf("different grader models (%q and %q): a different grader can move the number without the skill changing", r.GraderModel, o.GraderModel)
	}
	return true, ""
}

// samePanel compares two panels as a sorted multiset of (agent, model, class).
func samePanel(a, b []PanelMember) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := sortedKeys(a), sortedKeys(b)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func sortedKeys(ms []PanelMember) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Agent + "\x00" + m.Model + "\x00" + m.Class + "\x00" + m.Provider
	}
	sort.Strings(out)
	return out
}

// sameCLIVersions compares CLIVersion as a sorted multiset. Called after
// samePanel confirms the members match.
func sameCLIVersions(a, b []PanelMember) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := sortedVersions(a), sortedVersions(b)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func sortedVersions(ms []PanelMember) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.CLIVersion
	}
	sort.Strings(out)
	return out
}

// SetReport is what a set's members did beside the skill: which skill each
// probe went to, the share of the skill's prompts each member took, and the
// expectations and checks that pass alone and fail with the set.
type SetReport struct {
	Name         string            `json:"name"`
	ResolutionID string            `json:"resolution_id"`
	Members      []SetReportMember `json:"members"`
	// Probes lists every probe and the skills that fired on it.
	Probes []RoutedProbe `json:"probes,omitempty"`
	// Shares lists, per member, how many of the skill's own should-trigger
	// probes it took. Empty for a contest candidate, which has no single
	// skill under test.
	Shares []NeighbourShare `json:"shares,omitempty"`
	// RulesLost lists the expectations and checks that pass with the skill
	// alone and fail with the set, on the primary member.
	RulesLost []RuleLost `json:"rules_lost,omitempty"`
	// TokensMedian is the median token spend of the with-set runs.
	TokensMedian int64 `json:"tokens_median,omitempty"`
}

// SetReportMember is one pinned member of the resolution the eval installed.
type SetReportMember struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	Digest  string `json:"digest"`
}

// RoutedProbe is one probe and the skills that fired on it. Source is empty
// for a query from the suite under test, and names the member whose own
// should-trigger query it is otherwise.
type RoutedProbe struct {
	Prompt   string   `json:"prompt"`
	Positive bool     `json:"positive"`
	Source   string   `json:"source,omitempty"`
	Fired    []string `json:"fired,omitempty"`
	Unknown  bool     `json:"unknown,omitempty"`
}

// NeighbourShare is one member's take of the skill's prompts. Took counts the
// should-trigger probes where the member fired and the skill did not; Shared
// counts those where both fired. Share is Took over Of, the measured
// should-trigger probes.
type NeighbourShare struct {
	Name   string  `json:"name"`
	Took   int     `json:"took"`
	Shared int     `json:"shared"`
	Of     int     `json:"of"`
	Share  float64 `json:"share"`
}

// RuleLost is one expectation or check that passes with the skill alone and
// fails with the set. Passes and runs are counted on the primary member.
type RuleLost struct {
	TaskID      string `json:"task_id"`
	Text        string `json:"text"`
	Check       bool   `json:"check,omitempty"`
	AlonePasses int    `json:"alone_passes"`
	AloneRuns   int    `json:"alone_runs"`
	SetPasses   int    `json:"set_passes"`
	SetRuns     int    `json:"set_runs"`
}

// Shares counts, per member, the skill's should-trigger probes it took. A
// probe counts only when it is the suite's own and was measured.
func Shares(skill string, members []string, probes []RoutedProbe) []NeighbourShare {
	out := make([]NeighbourShare, 0, len(members))
	of := 0
	for _, p := range probes {
		if p.Positive && p.Source == "" && !p.Unknown {
			of++
		}
	}
	for _, m := range members {
		ns := NeighbourShare{Name: m, Of: of}
		for _, p := range probes {
			if !p.Positive || p.Source != "" || p.Unknown || !contains(p.Fired, m) {
				continue
			}
			if contains(p.Fired, skill) {
				ns.Shared++
			} else {
				ns.Took++
			}
		}
		if of > 0 {
			ns.Share = float64(ns.Took) / float64(of)
		}
		out = append(out, ns)
	}
	return out
}

// RulesLost compares each expectation's pass count with the skill alone and
// with the set, on the primary member. An expectation is lost when it passes
// in at least half its runs alone and in fewer than half with the set.
func RulesLost(tasks []TaskReport, primary string, alone, withSet store.Condition) []RuleLost {
	var out []RuleLost
	for _, t := range tasks {
		type tally struct{ ap, ar, sp, sr int }
		by := map[string]*tally{}
		var order []string
		checks := map[string]bool{}
		for _, g := range t.Grades {
			if g.Model != primary || (g.Condition != alone && g.Condition != withSet) {
				continue
			}
			for _, e := range g.Expectations {
				tl, ok := by[e.Text]
				if !ok {
					tl = &tally{}
					by[e.Text] = tl
					order = append(order, e.Text)
				}
				checks[e.Text] = e.Check
				pass := 0
				if e.Passed {
					pass = 1
				}
				if g.Condition == alone {
					tl.ap += pass
					tl.ar++
				} else {
					tl.sp += pass
					tl.sr++
				}
			}
		}
		for _, text := range order {
			tl := by[text]
			if tl.ar == 0 || tl.sr == 0 {
				continue
			}
			if 2*tl.ap >= tl.ar && 2*tl.sp < tl.sr {
				out = append(out, RuleLost{
					TaskID: t.TaskID, Text: text, Check: checks[text],
					AlonePasses: tl.ap, AloneRuns: tl.ar, SetPasses: tl.sp, SetRuns: tl.sr,
				})
			}
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
