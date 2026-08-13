package suite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skael-dev/skael/internal/eval/spec"
)

// EvalsDir is the eval set's directory name, at the skill root. It is
// Anthropic's layout, unchanged, so a skill directory moves between skael and
// skill-creator without a translation step.
const EvalsDir = "evals"

const (
	evalsFile    = "evals.json"
	triggersFile = "triggers.json"
	filesDir     = "files"
)

// Eval is one task the skill must handle, from evals/evals.json.
type Eval struct {
	ID             int      `json:"id"`
	Prompt         string   `json:"prompt"`
	ExpectedOutput string   `json:"expected_output,omitempty"`
	Files          []string `json:"files,omitempty"`
	Expectations   []string `json:"expectations,omitempty"`
}

// UnmarshalJSON reads `assertions` as an alias for `expectations`.
//
// Anthropic contradicts itself: references/schemas.md and grading.json say
// expectations, SKILL.md says assertions. We write expectations only, and
// accept both, so a file written by skill-creator loads either way.
func (e *Eval) UnmarshalJSON(b []byte) error {
	type plain Eval
	var raw struct {
		plain
		Assertions []string `json:"assertions"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*e = Eval(raw.plain)
	if len(e.Expectations) == 0 {
		e.Expectations = raw.Assertions
	}
	return nil
}

// EvalSet is the whole of evals/evals.json.
type EvalSet struct {
	SkillName string `json:"skill_name"`
	Evals     []Eval `json:"evals"`
}

// TriggerQuery is one entry of evals/triggers.json: a prompt, and whether the
// skill must fire on it.
//
// The file is a bare array rather than an object, which is the shape
// Anthropic's run_eval.py takes through --eval-set. Anthropic never names the
// file, so the name is ours and the contents are theirs.
type TriggerQuery struct {
	Query         string `json:"query"`
	ShouldTrigger bool   `json:"should_trigger"`
}

// TriggersFromSpec derives the trigger queries from an authored spec.
func TriggersFromSpec(sp *spec.SkillSpec) []TriggerQuery {
	out := make([]TriggerQuery, 0, len(sp.Triggers))
	for _, t := range sp.Triggers {
		out = append(out, TriggerQuery{Query: t.Text, ShouldTrigger: !t.Negative})
	}
	return out
}

// LoadEvalSet reads evals/evals.json from dir.
func LoadEvalSet(dir string) (*EvalSet, error) {
	path := filepath.Join(dir, EvalsDir, evalsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("suite: reading %s: %w", filepath.Join(EvalsDir, evalsFile), err)
	}
	var set EvalSet
	if err := json.Unmarshal(b, &set); err != nil {
		return nil, fmt.Errorf("suite: parsing %s: %w", filepath.Join(EvalsDir, evalsFile), err)
	}
	if len(set.Evals) == 0 {
		return nil, fmt.Errorf("suite: %s lists no evals", filepath.Join(EvalsDir, evalsFile))
	}

	// A duplicate id makes every per-eval figure in a report ambiguous, and an
	// eval with no prompt cannot be run at all. Both are refusals rather than
	// void results, because neither is a property of the skill under test.
	seen := make(map[int]bool, len(set.Evals))
	for _, e := range set.Evals {
		if seen[e.ID] {
			return nil, fmt.Errorf("suite: %s has two evals with id %d", evalsFile, e.ID)
		}
		seen[e.ID] = true
		if e.Prompt == "" {
			return nil, fmt.Errorf("suite: eval %d has no prompt", e.ID)
		}
	}
	return &set, nil
}

// LoadTriggerQueries reads evals/triggers.json from dir. A missing file is not
// an error: a skill can carry evals and no trigger set.
func LoadTriggerQueries(dir string) ([]TriggerQuery, error) {
	path := filepath.Join(dir, EvalsDir, triggersFile)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("suite: reading %s: %w", filepath.Join(EvalsDir, triggersFile), err)
	}
	var qs []TriggerQuery
	if err := json.Unmarshal(b, &qs); err != nil {
		return nil, fmt.Errorf("suite: parsing %s: %w", filepath.Join(EvalsDir, triggersFile), err)
	}
	for i, q := range qs {
		if q.Query == "" {
			return nil, fmt.Errorf("suite: %s entry %d has no query", triggersFile, i)
		}
	}
	return qs, nil
}

// WriteEvalSet writes evals/evals.json under dir.
func WriteEvalSet(dir string, set *EvalSet) error {
	if err := os.MkdirAll(filepath.Join(dir, EvalsDir), dirMode); err != nil {
		return fmt.Errorf("suite: creating %s: %w", EvalsDir, err)
	}
	b, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return fmt.Errorf("suite: marshalling %s: %w", evalsFile, err)
	}
	return os.WriteFile(filepath.Join(dir, EvalsDir, evalsFile), append(b, '\n'), fileMode)
}

// WriteTriggerQueries writes evals/triggers.json under dir.
func WriteTriggerQueries(dir string, qs []TriggerQuery) error {
	if err := os.MkdirAll(filepath.Join(dir, EvalsDir), dirMode); err != nil {
		return fmt.Errorf("suite: creating %s: %w", EvalsDir, err)
	}
	if qs == nil {
		qs = []TriggerQuery{}
	}
	b, err := json.MarshalIndent(qs, "", "  ")
	if err != nil {
		return fmt.Errorf("suite: marshalling %s: %w", triggersFile, err)
	}
	return os.WriteFile(filepath.Join(dir, EvalsDir, triggersFile), append(b, '\n'), fileMode)
}

// EvalCheck is one eval's static validation result. It replaces the oracle
// gate: with no verifier script to prove correct, what remains to check is
// that the eval can be run and can be scored at all.
type EvalCheck struct {
	ID     int
	OK     bool
	Void   bool
	Reason string
}

// Validate checks every eval against dir, which holds the evals/ directory.
// A void eval is planned but never scored, so a suite does not silently lose
// its denominator.
func Validate(dir string, set *EvalSet) []EvalCheck {
	out := make([]EvalCheck, 0, len(set.Evals))
	for _, e := range set.Evals {
		check := EvalCheck{ID: e.ID, OK: true}
		switch {
		case len(e.Expectations) == 0:
			check = EvalCheck{ID: e.ID, Void: true, Reason: "no expectations to grade"}
		default:
			for _, f := range e.Files {
				if missing := missingFile(dir, f); missing != "" {
					check = EvalCheck{ID: e.ID, Void: true, Reason: "missing input file " + missing}
					break
				}
			}
		}
		out = append(out, check)
	}
	return out
}

// missingFile returns rel when it does not resolve to a regular file inside
// dir, and "" when it does. A path that escapes dir counts as missing: the
// files list is model-authored, so it is untrusted input.
func missingFile(dir, rel string) string {
	if rel == "" {
		return "(empty path)"
	}
	target := filepath.Join(dir, filepath.FromSlash(rel))
	within, err := filepath.Rel(dir, target)
	if err != nil || filepath.IsAbs(rel) || within == ".." ||
		strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return rel
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return rel
	}
	return ""
}

// FilesDir is where an eval's input files live, relative to the skill root.
func FilesDir() string { return filepath.Join(EvalsDir, filesDir) }
