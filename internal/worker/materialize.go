package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/eval/contract"
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
	// Spec is the authored spec.SkillSpec this suite was checked against
	// (see evalsuite.Registry.Put's specJSON). Nil when the suite predates
	// this field, or the pusher genuinely sent none — Materialize falls back
	// to a placeholder reconstructed from the bundle's SKILL.md frontmatter
	// in that case, and logs loudly when it does, because that fallback
	// silently drops the skill's real deps and purpose.
	Spec *spec.SkillSpec
	// WantSuiteRef, if set, is checked against the ref of the materialized
	// suite tree before anything else runs. A mismatch here means the job's
	// suite_ref and the archive FetchSuite actually returned disagree —
	// failing fast catches that before an evaluation runs against it for
	// however long, only to be rejected when the score is posted.
	WantSuiteRef string
}

// Materialize builds a whetstone workspace at dir from a downloaded bundle
// and suite, recording the registry's checks so the eval's oracle gate is
// satisfied by the author's recorded run rather than bypassed.
func Materialize(dir string, in MaterializeInput) (_ *store.Store, err error) {
	st, err := store.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("worker: materialize open store: %w", err)
	}
	// Every error return below leaves st open unless closed here: dir gets
	// removed by the caller regardless, but that only deletes the directory
	// entries — this process keeps its open fds on the deleted db/WAL/SHM
	// files until the handle itself is closed. A worker retrying the same
	// kind of failure (a bundle without SKILL.md, a corrupt archive) up to
	// max_attempts leaks three fds per attempt without this.
	defer func() {
		if err != nil {
			_ = st.Close()
		}
	}()

	// Into the skill dir itself: that is what RunEvalWith hands the runner as
	// BundleDir and what the adapter installs into the sandbox. Unpacking to a
	// temp dir left it holding only spec.yaml and the eval sidecar, so the
	// panel ran with no SKILL.md and scored a skill that was never installed.
	// lint.Excluded keeps both out of a packed archive, so nothing here can
	// clobber what Materialize writes around it.
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
		// The suite's spec travelled from a different push than this job's
		// skill name necessarily agrees with; keep the workspace keyed
		// consistently on the name the job actually names.
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

	// The contract is compiled, not shipped: `whetstone new` writes it into the
	// eval sidecar, which lint.Excluded keeps out of every published bundle. A
	// worker therefore has to compile its own, and contract.Compile is a pure
	// function of the spec saved above. Omitting it fails only at the scoring
	// step, after the panel has already run.
	if err := writeContract(st, in.Skill, sp); err != nil {
		return nil, err
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
	if err := st.SaveSuiteCheck(in.Skill, ref, rows); err != nil {
		return nil, fmt.Errorf("worker: materialize save suite checks: %w", err)
	}

	return st, nil
}

// writeContract mirrors `whetstone new`'s writeContract, which is the
// authoring path that produces the file a materialized workspace lacks.
func writeContract(st *store.Store, skillName string, sp *spec.SkillSpec) error {
	c, err := contract.Compile(sp)
	if err != nil {
		return fmt.Errorf("worker: materialize compile contract: %w", err)
	}

	path, err := st.ContractPath(skillName)
	if err != nil {
		return fmt.Errorf("worker: materialize contract path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("worker: materialize mkdir contract dir: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("worker: materialize create contract: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := c.Save(f); err != nil {
		return fmt.Errorf("worker: materialize save contract: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("worker: materialize writing %s: %w", path, err)
	}
	return nil
}

// specFromBundle reconstructs just enough of a spec.SkillSpec from the
// bundle's SKILL.md frontmatter to pass spec.Validate. It exists only as a
// fallback for a suite pushed with no spec recorded — see MaterializeInput's
// doc comment.
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

// unmarshalSuiteSpec decodes the JSON a suite record carries in its Spec
// field. Returns (nil, nil) when specJSON is empty or the JSON literal null
// — a jsonb column holding SQL NULL round-trips through encoding/json as the
// 4-byte literal "null", not as an empty RawMessage, so both must be treated
// as "no spec recorded". Without this, the literal null unmarshals into a
// non-nil but empty *SkillSpec, Materialize takes the "spec provided" branch,
// Validate() fails, and the job burns every retry instead of falling back to
// specFromBundle.
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
