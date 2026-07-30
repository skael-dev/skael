package eval_test

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/drift"
	"github.com/skael-dev/skael/internal/eval/lint"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
)

type expectedFinding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	File     string `json:"file"`
}

func archetypes(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("testdata", "corpus"))
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	if len(out) != 3 {
		t.Fatalf("corpus has %d archetypes, want 3", len(out))
	}
	return out
}

func TestCorpus_LintOutputIsStable(t *testing.T) {
	for _, name := range archetypes(t) {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("testdata", "corpus", name)

			res, err := lint.Run(dir)
			if err != nil {
				t.Fatalf("lint.Run: %v", err)
			}

			got := make([]expectedFinding, 0, len(res.Findings))
			for _, f := range res.Findings {
				got = append(got, expectedFinding{Rule: f.Rule, Severity: string(f.Severity), File: f.File})
			}
			sortFindings(got)

			raw, err := os.ReadFile(filepath.Join(dir, "expected-lint.json"))
			if err != nil {
				t.Fatalf("read expectation: %v", err)
			}
			var want []expectedFinding
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatalf("parse expectation: %v", err)
			}
			sortFindings(want)

			if len(got) != len(want) {
				t.Fatalf("lint produced %d findings, expected %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("finding %d = %+v, want %+v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestCorpus_SpecsAreValidAndCompile(t *testing.T) {
	for _, name := range archetypes(t) {
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", "corpus", name, "spec.yaml"))
			if err != nil {
				t.Fatalf("open spec: %v", err)
			}
			defer f.Close()

			sp, err := spec.Load(f)
			if err != nil {
				t.Fatalf("spec.Load: %v", err)
			}
			if errs := sp.Validate(); len(errs) != 0 {
				t.Fatalf("corpus spec is invalid: %v", errs)
			}

			c, err := contract.Compile(sp)
			if err != nil {
				t.Fatalf("contract.Compile: %v", err)
			}
			// Every archetype must yield something checkable, or the contract
			// carries no deterministic signal at all.
			if len(c.Steps) == 0 && len(c.Forbid) == 0 {
				t.Errorf("contract has no deterministic matchers: %+v", c)
			}

			// checkpointed-workflow specifically must yield BOTH kinds: it
			// exists to pin that a MUST-NOT constraint compiles to a forbid
			// rule, not just that the spec's steps produce step matchers. The
			// generic disjunction above would still pass if classifyForbid
			// silently stopped firing and the constraint was demoted to a
			// SemanticRule instead.
			if name == "checkpointed-workflow" {
				if len(c.Steps) == 0 {
					t.Errorf("no step matchers: %+v", c)
				}
				if len(c.Forbid) == 0 {
					t.Errorf("MUST-NOT constraint did not compile to a forbid rule: %+v", c)
				}
			}
		})
	}
}

func TestCorpus_OneArchetypeIsCleanAndOneIsNot(t *testing.T) {
	// The corpus is only a regression net if it exercises both outcomes. Three
	// bundles that all lint clean would not catch a linter that stopped firing.
	var clean, dirty int
	for _, name := range archetypes(t) {
		res, err := lint.Run(filepath.Join("testdata", "corpus", name))
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Findings) == 0 {
			clean++
		} else {
			dirty++
		}
	}
	if clean == 0 {
		t.Error("no archetype lints completely clean")
	}
	if dirty == 0 {
		t.Error("no archetype produces findings; a silent linter would pass this corpus")
	}
}

func sortFindings(f []expectedFinding) {
	sort.Slice(f, func(i, j int) bool {
		if f[i].File != f[j].File {
			return f[i].File < f[j].File
		}
		if f[i].Rule != f[j].Rule {
			return f[i].Rule < f[j].Rule
		}
		return f[i].Severity < f[j].Severity
	})
}

// TestCorpus_ExercisesTheBrokenLinkRule proves the corpus actually feeds the
// broken-link rule an input. A corpus whose SKILL.md files contain no relative
// markdown link at all would pass every expectation above while leaving the
// rule entirely unexercised — so this copies an archetype, removes the file its
// link resolves to, and requires the finding to appear.
func TestCorpus_ExercisesTheBrokenLinkRule(t *testing.T) {
	src := filepath.Join("testdata", "corpus", "document-formatter")
	dst := filepath.Join(t.TempDir(), "document-formatter")
	copyTree(t, src, dst)

	res, err := lint.Run(dst)
	if err != nil {
		t.Fatalf("lint.Run: %v", err)
	}
	if hasRule(res, "broken-link") {
		t.Fatalf("the unmodified corpus already reports a broken link: %+v", res.Findings)
	}

	if err := os.Remove(filepath.Join(dst, "references", "style-guide.md")); err != nil {
		t.Fatal(err)
	}

	res, err = lint.Run(dst)
	if err != nil {
		t.Fatalf("lint.Run: %v", err)
	}
	if !hasRule(res, "broken-link") {
		t.Errorf("removing a linked corpus file produced no broken-link finding: %+v", res.Findings)
	}
}

func hasRule(res *lint.Result, rule string) bool {
	for _, f := range res.Findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// expectedScore is the committed band for one archetype. Bands rather than exact
// values because the pipeline composes floating-point aggregates — but narrow
// bands, because a range wide enough to accept any behaviour asserts nothing.
type expectedScore struct {
	HeadlineMin   float64  `json:"headline_min"`
	HeadlineMax   float64  `json:"headline_max"`
	DriftGrade    string   `json:"drift_grade"`
	MinViolations int      `json:"min_violations"`
	Violations    []string `json:"violation_ids"`
	Unevaluable   int      `json:"unevaluable"`
	// RobustnessGapMin/Max bound strong.Drift.Mean - floor.Drift.Mean (see
	// drift.RobustnessGap). Committed here because it is the primary input
	// to the repair loop: an archetype whose two panel members drift
	// identically cannot regression-test whether that gap is measured at
	// all.
	RobustnessGapMin float64 `json:"robustness_gap_min"`
	RobustnessGapMax float64 `json:"robustness_gap_max"`
}

func TestCorpus_ScoresWithinItsCommittedBand(t *testing.T) {
	for _, name := range corpusArchetypes(t) {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("testdata", "corpus", name)
			want := loadExpectedScore(t, filepath.Join(dir, "eval", "expected-score.json"))
			c := loadContract(t, filepath.Join(dir, "eval", "contract.yaml"))

			// Recorded trajectories per (task, model, condition, attempt), replayed
			// through the real scoring path. No sandbox, no gateway: this is the
			// regression signal that survives in CI.
			rep := scoreFixtures(t, dir, c)

			if rep.Headline < want.HeadlineMin || rep.Headline > want.HeadlineMax {
				t.Errorf("headline %.1f outside the committed band [%.1f, %.1f]", rep.Headline, want.HeadlineMin, want.HeadlineMax)
			}
			for _, m := range rep.Members {
				if m.DriftGrade != want.DriftGrade {
					t.Errorf("%s drift grade %q, want %q", m.Member.Model, m.DriftGrade, want.DriftGrade)
				}
			}
			if got := violationIDs(rep); !equalSets(got, want.Violations) {
				t.Errorf("violations = %v, want %v", got, want.Violations)
			}
			// A change that starts silently skipping checks would otherwise look
			// like an improvement.
			if rep.Unevaluable != want.Unevaluable {
				t.Errorf("unevaluable = %d, want %d", rep.Unevaluable, want.Unevaluable)
			}
			if rep.RobustnessGap == nil {
				t.Errorf("robustness gap not computed; want a value in [%.1f, %.1f]", want.RobustnessGapMin, want.RobustnessGapMax)
			} else if *rep.RobustnessGap < want.RobustnessGapMin || *rep.RobustnessGap > want.RobustnessGapMax {
				t.Errorf("robustness gap %.2f outside the committed band [%.1f, %.1f]", *rep.RobustnessGap, want.RobustnessGapMin, want.RobustnessGapMax)
			}
		})
	}
}

func TestCorpus_TheThreeArchetypesScoreDifferently(t *testing.T) {
	var headlines []float64
	for _, name := range corpusArchetypes(t) {
		dir := filepath.Join("testdata", "corpus", name)
		headlines = append(headlines, scoreFixtures(t, dir, loadContract(t, filepath.Join(dir, "eval", "contract.yaml"))).Headline)
	}
	// If all three land on the same number the fixtures are not exercising the
	// pipeline — they are exercising its defaults. A clean transform, a
	// formatter that writes outside its scope, and a workflow that skips a
	// checkpoint must produce three different scores.
	for i := range headlines {
		for j := i + 1; j < len(headlines); j++ {
			if math.Abs(headlines[i]-headlines[j]) < 1 {
				t.Errorf("archetypes %d and %d scored %.1f and %.1f; the fixtures do not discriminate", i, j, headlines[i], headlines[j])
			}
		}
	}
}

func TestCorpus_ReportRoundTripsAndIsSelfComparable(t *testing.T) {
	dir := filepath.Join("testdata", "corpus", "deterministic-transform")
	rep := scoreFixtures(t, dir, loadContract(t, filepath.Join(dir, "eval", "contract.yaml")))

	var buf bytes.Buffer
	if err := rep.Save(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := report.Load(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if ok, why := rep.Comparable(back); !ok {
		t.Errorf("a report is not comparable to its own round trip: %s", why)
	}
	var html bytes.Buffer
	if err := back.HTML(&html); err != nil {
		t.Fatalf("HTML: %v", err)
	}
	if html.Len() == 0 {
		t.Error("the HTML report rendered empty")
	}
}

// corpusArchetypes is archetypes under another name: TestCorpus_ScoresWithinItsCommittedBand
// and its siblings score the same three bundles TestCorpus_LintOutputIsStable does, so they
// share the discovery helper rather than re-implementing "read testdata/corpus".
func corpusArchetypes(t *testing.T) []string {
	return archetypes(t)
}

func loadExpectedScore(t *testing.T, path string) expectedScore {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read expected score: %v", err)
	}
	var want expectedScore
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("parse expected score: %v", err)
	}
	return want
}

func loadContract(t *testing.T, path string) *contract.Contract {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open contract: %v", err)
	}
	defer func() { _ = f.Close() }()
	c, err := contract.Load(f)
	if err != nil {
		t.Fatalf("load contract: %v", err)
	}
	return c
}

// fixtureRunKey is a trajectory file's basename, parsed into the RunKey it
// records. The corpus names each recorded run
// <task>__<agent>__<model>__<condition>__<attempt>.jsonl, with a sibling
// <the-same-basename>.grading.json — that pairing is scoreFixtures' entire
// contract with the fixture files, so a rename on either side breaks loudly
// here rather than silently skipping a run.
func fixtureRunKey(t *testing.T, base string) store.RunKey {
	t.Helper()
	parts := strings.Split(base, "__")
	if len(parts) != 5 {
		t.Fatalf("fixture file name %q does not parse into task__agent__model__condition__attempt", base)
	}
	attempt, err := strconv.Atoi(parts[4])
	if err != nil {
		t.Fatalf("fixture file name %q has a non-numeric attempt: %v", base, err)
	}
	return store.RunKey{TaskID: parts[0], Agent: parts[1], Model: parts[2], Condition: parts[3], Attempt: attempt}
}

// fixtureClass classifies a fixture model name into the capability tier it
// stands for, by the corpus's own naming convention (model-strong /
// model-floor). This is fixture-only bookkeeping, not a real model registry.
func fixtureClass(model string) spec.ModelTier {
	if strings.Contains(model, "strong") {
		return spec.TierStrong
	}
	return spec.TierFloor
}

// memberAgg accumulates one panel member's raw measurements across every run
// scoreFixtures reads for it, before they are reduced into score.Pillars and
// drift.Result values.
type memberAgg struct {
	class spec.ModelTier

	skillPassed, skillTotal       int
	baselinePassed, baselineTotal int
	skillTokens, baselineTokens   []int64

	drift      []drift.Result
	runDrift   []report.RunDrift
	unevalTot  int
	unevalDets []string
}

// scoreFixtures replays a corpus archetype's recorded trajectories through
// the real scoring path: it walks dir/eval/trajectories for runs named
// <task>__<agent>__<model>__<condition>__<attempt>.jsonl, loads each run's
// events with runner.LoadEvents and its grading.json with runner.LoadGrading,
// scores every skill-condition run's events against c with drift.Observe and
// drift.Score, and derives each panel member's four Effectiveness pillars
// from the recorded verifier exits and token counts.
//
// There is no gateway and no judge in this mode, so Uplift always comes from
// score.UpliftFromPassRates (the documented fallback path) and the drift
// engine's semantic component — the one part of Adherence a judge would
// normally score — is fixed at 1, i.e. every judge-scored rule the corpus
// declares is treated as satisfied. That is a real limitation of fixture-mode
// scoring, not an attempt to hide it: a report built this way can never catch
// a semantic-rubric regression, only a deterministic one.
func scoreFixtures(t *testing.T, dir string, c *contract.Contract) *report.Report {
	t.Helper()

	trajDir := filepath.Join(dir, "eval", "trajectories")
	entries, err := os.ReadDir(trajDir)
	if err != nil {
		t.Fatalf("read trajectories: %v", err)
	}

	members := map[string]*memberAgg{}
	task := ""

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".jsonl")
		key := fixtureRunKey(t, base)
		task = key.TaskID

		events, err := runner.LoadEvents(filepath.Join(trajDir, e.Name()))
		if err != nil {
			t.Fatalf("LoadEvents %s: %v", e.Name(), err)
		}
		g, err := runner.LoadGrading(filepath.Join(trajDir, base+".grading.json"))
		if err != nil {
			t.Fatalf("LoadGrading %s: %v", base, err)
		}
		if g.VerifierExit == nil {
			t.Fatalf("fixture %s has no recorded verifier exit", base)
		}
		passed := *g.VerifierExit == 0
		tokens := g.Meta.InputTokens + g.Meta.OutputTokens

		m := members[key.Model]
		if m == nil {
			m = &memberAgg{class: fixtureClass(key.Model)}
			members[key.Model] = m
		}

		switch key.Condition {
		case runner.CondSkill:
			m.skillTotal++
			if passed {
				m.skillPassed++
			}
			m.skillTokens = append(m.skillTokens, tokens)

			obs, err := drift.Observe(c, events)
			if err != nil {
				t.Fatalf("drift.Observe %s: %v", base, err)
			}
			res, err := drift.Score(obs, 1, drift.DefaultWeights)
			if err != nil {
				t.Fatalf("drift.Score %s: %v", base, err)
			}
			m.drift = append(m.drift, res)
			m.runDrift = append(m.runDrift, report.RunDrift{
				Agent: key.Agent, Model: key.Model, Attempt: key.Attempt,
				Result: res, Violations: obs.Violations,
			})
			m.unevalTot += obs.Unevaluable
			m.unevalDets = append(m.unevalDets, obs.UnevaluableDetail...)
		case runner.CondBaseline:
			m.baselineTotal++
			if passed {
				m.baselinePassed++
			}
			m.baselineTokens = append(m.baselineTokens, tokens)
		default:
			t.Fatalf("fixture %s has unexpected condition %q", base, key.Condition)
		}
	}
	if task == "" {
		t.Fatalf("no trajectory fixtures found under %s", trajDir)
	}

	var modelNames []string
	for name := range members {
		modelNames = append(modelNames, name)
	}
	sort.Strings(modelNames)

	var (
		memberInputs   []report.MemberInput
		panel          []report.PanelMember
		conditions     []report.ConditionReport
		taskDrift      []report.RunDrift
		totalUneval    int
		unevalDetail   []string
		agentNameFound = "claude-code"
	)

	for _, name := range modelNames {
		m := members[name]

		skillPassRate := rate(m.skillPassed, m.skillTotal)
		baselinePassRate := rate(m.baselinePassed, m.baselineTotal)

		eff, err := score.Efficiency(median(m.skillTokens), median(m.baselineTokens))
		if err != nil {
			t.Fatalf("score.Efficiency for %s: %v", name, err)
		}

		pillars := score.Pillars{
			// Trigger measurement uses a separate probe condition this corpus
			// does not exercise; fixed at 1 rather than left to drift, so a
			// change in headline is legible as a change in the pillars this
			// fixture does measure.
			TriggerF1:   1,
			Reliability: skillPassRate,
			Uplift:      score.UpliftFromPassRates(skillPassRate, baselinePassRate),
			Efficiency:  eff,
		}

		panelMember := report.PanelMember{Agent: agentNameFound, Model: name, Class: string(m.class), CLIVersion: "fixture"}
		panel = append(panel, panelMember)
		memberInputs = append(memberInputs, report.MemberInput{
			Member:  panelMember,
			Pillars: pillars,
			Healthy: true,
			Drift:   m.drift,
		})

		conditions = append(conditions,
			report.ConditionReport{Condition: runner.CondSkill, Model: name, Passes: m.skillPassed, Runs: m.skillTotal},
			report.ConditionReport{Condition: runner.CondBaseline, Model: name, Passes: m.baselinePassed, Runs: m.baselineTotal},
		)
		taskDrift = append(taskDrift, m.runDrift...)
		totalUneval += m.unevalTot
		unevalDetail = append(unevalDetail, m.unevalDets...)
	}

	in := report.ComposeInput{
		Skill:         filepath.Base(dir),
		SpecVersion:   1,
		Tier:          "smoke",
		SuiteRef:      "corpus-fixture",
		EngineVersion: "test",

		ModelPanel:    panel,
		PanelComplete: true,

		Members: memberInputs,
		Tasks: []report.TaskInput{{
			TaskID:     task,
			Kind:       "core",
			Split:      "dev",
			Conditions: conditions,
			Drift:      taskDrift,
		}},

		// No gateway in fixture mode: the judge was never calibrated, so
		// Uplift always demotes to the pass-rate fallback.
		JudgeTrusted: false,

		Unevaluable:       totalUneval,
		UnevaluableDetail: unevalDetail,
	}

	rep, err := report.Compose(in)
	if err != nil {
		t.Fatalf("report.Compose: %v", err)
	}
	return rep
}

func rate(passed, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(passed) / float64(total)
}

func median(vals []int64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := append([]int64(nil), vals...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	n := len(sorted)
	if n%2 == 1 {
		return float64(sorted[n/2])
	}
	return float64(sorted[n/2-1]+sorted[n/2]) / 2
}

// violationIDs collects the distinct forbid-rule IDs any run in rep violated,
// across every task and every member.
func violationIDs(rep *report.Report) []string {
	seen := map[string]bool{}
	for _, task := range rep.Tasks {
		for _, rd := range task.Drift {
			for _, v := range rd.Violations {
				seen[v.ID] = true
			}
		}
	}
	var out []string
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func equalSets(a, b []string) bool {
	as, bs := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}
