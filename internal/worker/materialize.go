package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/skill"
)

// MaterializeInput is what Materialize needs to build one job's workspace.
type MaterializeInput struct {
	Skill        string
	Bundle       []byte
	SuiteArchive []byte
	Checks       []evalsuite.Check
	// Spec is nil when the suite predates this field; Materialize falls back
	// to a placeholder from SKILL.md frontmatter.
	Spec         *spec.SkillSpec
	WantSuiteRef string
}

// Materialize builds a whetstone workspace at dir from a downloaded bundle
// and suite.
func Materialize(dir string, in MaterializeInput) (_ *store.Store, err error) {
	st, err := store.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("worker: materialize open store: %w", err)
	}
	defer func() {
		if err != nil {
			_ = st.Close()
		}
	}()

	skillDir, err := st.SkillDir(in.Skill)
	if err != nil {
		return nil, fmt.Errorf("worker: materialize skill dir: %w", err)
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return nil, fmt.Errorf("worker: materialize mkdir skill dir: %w", err)
	}
	if err := skill.Unpack(bytes.NewReader(in.Bundle), skillDir); err != nil {
		return nil, fmt.Errorf("worker: materialize unpack bundle: %w", err)
	}

	sp := in.Spec
	if sp == nil {
		log.Warn().Str("skill", in.Skill).Msg(
			"worker: materialize: no spec recorded for this suite; falling back to a placeholder " +
				"reconstructed from SKILL.md frontmatter — the skill's real deps and purpose are lost, " +
				"and the sandbox this eval runs in will not have them")
		sp, err = specFromBundle(skillDir, in.Skill)
		if err != nil {
			return nil, err
		}
	} else {
		sp.Name = in.Skill
		if errs := sp.Validate(); len(errs) > 0 {
			return nil, fmt.Errorf("worker: materialize: spec recorded for suite is invalid: %v", errs)
		}
	}

	specVersion, err := st.SaveSpec(sp)
	if err != nil {
		return nil, fmt.Errorf("worker: materialize save spec: %w", err)
	}
	if err := st.ApproveSpec(in.Skill, specVersion); err != nil {
		return nil, fmt.Errorf("worker: materialize approve spec: %w", err)
	}

	suiteDir, err := st.SuiteDir(in.Skill)
	if err != nil {
		return nil, fmt.Errorf("worker: materialize suite dir: %w", err)
	}
	if err := os.MkdirAll(suiteDir, 0o755); err != nil {
		return nil, fmt.Errorf("worker: materialize mkdir suite dir: %w", err)
	}
	if err := evalsuite.Unpack(in.SuiteArchive, suiteDir); err != nil {
		return nil, fmt.Errorf("worker: materialize unpack suite: %w", err)
	}

	ref, err := suite.Ref(suiteDir)
	if err != nil {
		return nil, fmt.Errorf("worker: materialize suite ref: %w", err)
	}
	if in.WantSuiteRef != "" && ref != in.WantSuiteRef {
		return nil, fmt.Errorf("worker: materialize: suite ref %s does not match the requested ref %s", ref, in.WantSuiteRef)
	}

	rows := make([]store.SuiteCheckRow, len(in.Checks))
	for i, c := range in.Checks {
		rows[i] = store.SuiteCheckRow{TaskID: c.TaskID, Void: c.Void, Reason: c.Reason}
	}
	if err := st.SaveSuiteCheck(in.Skill, ref, rows); err != nil {
		return nil, fmt.Errorf("worker: materialize save suite checks: %w", err)
	}

	return st, nil
}

// specFromBundle reconstructs a minimal spec from the bundle's frontmatter.
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

// unmarshalSuiteSpec decodes the JSON a suite record carries. Returns
// (nil, nil) when empty or the JSON literal null, both of which a jsonb
// column can produce.
func unmarshalSuiteSpec(specJSON json.RawMessage) (*spec.SkillSpec, error) {
	if len(specJSON) == 0 || string(bytes.TrimSpace(specJSON)) == "null" {
		return nil, nil
	}
	var sp spec.SkillSpec
	if err := json.Unmarshal(specJSON, &sp); err != nil {
		return nil, fmt.Errorf("worker: unmarshal suite spec: %w", err)
	}
	return &sp, nil
}
