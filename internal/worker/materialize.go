package worker

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/skill"
)

// Materialize builds a whetstone workspace at dir from a downloaded bundle
// and suite, recording the registry's checks so the eval's oracle gate is
// satisfied by the author's recorded run rather than bypassed.
//
// A published bundle never carries the authored spec.yaml — lint.Excluded
// strips it (and the whole eval sidecar) before packing, on purpose: it is
// authoring scaffolding, not shipped skill content. So the spec RunEvalWith
// gates on here is necessarily a stand-in reconstructed from the bundle's
// SKILL.md frontmatter, just complete enough to satisfy spec.Validate — the
// same technique store.skillDirName uses to turn an arbitrary skill name into
// a legal directory name. It carries the real name and description; every
// other field is a placeholder. Approving it only tells RunEvalWith "the
// worker is allowed to run this skill", never "a human reviewed this text".
func Materialize(dir, skillName string, bundle, suiteArchive []byte, checks []evalsuite.Check) (*store.Store, error) {
	st, err := store.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("worker: materialize open store: %w", err)
	}

	bundleDir, err := os.MkdirTemp("", "skael-worker-bundle-*")
	if err != nil {
		return nil, fmt.Errorf("worker: materialize temp bundle dir: %w", err)
	}
	defer os.RemoveAll(bundleDir)

	if err := skill.Unpack(bytes.NewReader(bundle), bundleDir); err != nil {
		return nil, fmt.Errorf("worker: materialize unpack bundle: %w", err)
	}

	sp, err := specFromBundle(bundleDir, skillName)
	if err != nil {
		return nil, err
	}

	specVersion, err := st.SaveSpec(sp)
	if err != nil {
		return nil, fmt.Errorf("worker: materialize save spec: %w", err)
	}
	if err := st.ApproveSpec(skillName, specVersion); err != nil {
		return nil, fmt.Errorf("worker: materialize approve spec: %w", err)
	}

	suiteDir, err := st.SuiteDir(skillName)
	if err != nil {
		return nil, fmt.Errorf("worker: materialize suite dir: %w", err)
	}
	if err := os.MkdirAll(suiteDir, 0o755); err != nil {
		return nil, fmt.Errorf("worker: materialize mkdir suite dir: %w", err)
	}
	if err := evalsuite.Unpack(suiteArchive, suiteDir); err != nil {
		return nil, fmt.Errorf("worker: materialize unpack suite: %w", err)
	}

	ref, err := suite.Ref(suiteDir)
	if err != nil {
		return nil, fmt.Errorf("worker: materialize suite ref: %w", err)
	}

	rows := make([]store.SuiteCheckRow, len(checks))
	for i, c := range checks {
		// SuiteCheckRow has no field for evalsuite.Check.OK: RunEvalWith's
		// oracle gate (cli/whetstone/eval.go) only ever reads Void — it uses
		// a check's presence to know the task was gated at all, and Void to
		// know whether to exclude it. OK is not dropped by mistake; the row
		// simply has nothing to preserve it into, and nothing downstream
		// consults it. Void and Reason are copied verbatim, not derived from
		// OK — a check can be OK:false and Void:false (the oracle failed but
		// the task itself is not stale), and treating that as void here would
		// silently exclude a task that recording a check for said should
		// still be scored.
		rows[i] = store.SuiteCheckRow{TaskID: c.TaskID, Void: c.Void, Reason: c.Reason}
	}
	if err := st.SaveSuiteCheck(skillName, ref, rows); err != nil {
		return nil, fmt.Errorf("worker: materialize save suite checks: %w", err)
	}

	return st, nil
}

// specFromBundle reconstructs just enough of a spec.SkillSpec from the
// bundle's SKILL.md frontmatter to pass spec.Validate. See Materialize's doc
// comment for why the real authored spec is unavailable here.
func specFromBundle(bundleDir, skillName string) (*spec.SkillSpec, error) {
	raw, err := os.ReadFile(filepath.Join(bundleDir, "SKILL.md"))
	if err != nil {
		return nil, fmt.Errorf("worker: materialize read SKILL.md: %w", err)
	}

	fm, _, err := skill.ParseFrontmatter(string(raw))
	if err != nil {
		return nil, fmt.Errorf("worker: materialize parse SKILL.md frontmatter: %w", err)
	}

	description := "materialized for evaluation"
	if fm != nil {
		if d, ok := fm["description"].(string); ok && d != "" {
			description = d
		}
	}

	sp := &spec.SkillSpec{
		Name:        skillName,
		Purpose:     description,
		Description: description,
		Triggers:    []spec.TriggerPhrase{{Text: "probe"}},
		Steps:       []spec.Step{{ID: "s1", Action: "probe", Postcondition: "probe"}},
		TargetTier:  spec.TierMid,
	}
	if errs := sp.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("worker: materialize reconstructed spec is invalid: %v", errs)
	}
	return sp, nil
}
