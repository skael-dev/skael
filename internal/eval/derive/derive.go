// Package derive builds an evaluation suite for a skill that has none. It is
// the path an imported or hand-published skill takes: whetstone's authoring
// flow produces a spec.SkillSpec through a human interview, and a published
// bundle carries no spec.yaml, so everything downstream of the IR has to be
// reconstructed before a skill can be measured at all.
//
// It lives here rather than in cmd/skael-worker because it needs tests, and
// it is separate from internal/worker so that package keeps needing neither
// an LLM nor Docker.
package derive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalsuite"
	skillpkg "github.com/skael-dev/skael/internal/skill"
)

// evalCount is how many evals a derived set asks for. More than the full
// tier's budget of 10: there is no author to fix an eval that validation
// voids, so the surplus is what lets runner.BuildPlan still plan afterwards.
const evalCount = 14

// Options are the deriver's injected dependencies. There is no sandbox here
// any more: the oracle gate was the only part that needed one.
type Options struct {
	Gateway llm.Gateway
	Logger  func(format string, args ...any)
}

// Input is one derivation request.
type Input struct {
	Skill  string
	Bundle []byte
	// Tier and Panel are used only to dry-run runner.BuildPlan, so a suite too
	// thin to plan is refused before it is pushed and an eval row created.
	Tier  string
	Panel runner.Panel
}

// Result is a derived suite, ready to push to the registry.
type Result struct {
	Archive []byte
	Checks  []evalsuite.Check
	Spec    *spec.SkillSpec
}

// Deriver builds suites. Construct with New.
type Deriver struct{ o Options }

// New validates o and returns a Deriver.
func New(o Options) (*Deriver, error) {
	if o.Gateway == nil {
		return nil, errors.New("derive: New requires a non-nil Gateway")
	}
	if o.Logger == nil {
		o.Logger = func(string, ...any) {}
	}
	return &Deriver{o: o}, nil
}

// Derive recovers a spec from the bundle, drafts an eval set from it, and
// validates that set statically.
//
// There is no oracle gate any more: with no verifier script to prove correct,
// what remains to check is that each eval can be run and scored, which needs
// neither Docker nor a staged workspace.
func (d *Deriver) Derive(ctx context.Context, in Input) (*Result, error) {
	bundleDir, err := os.MkdirTemp("", "derive-bundle-*")
	if err != nil {
		return nil, fmt.Errorf("derive: temp dir: %w", err)
	}
	defer os.RemoveAll(bundleDir)
	if err := skillpkg.Unpack(bytes.NewReader(in.Bundle), bundleDir); err != nil {
		return nil, fmt.Errorf("derive: unpack bundle: %w", err)
	}

	sp, err := spec.Recover(ctx, d.o.Gateway, in.Skill, bundleDir)
	if err != nil {
		return nil, fmt.Errorf("derive: recover spec: %w", err)
	}

	set, triggers, err := suite.Generate(ctx, d.o.Gateway, sp, evalCount)
	if err != nil {
		return nil, fmt.Errorf("derive: generate eval set: %w", err)
	}

	suiteDir, err := os.MkdirTemp("", "derive-suite-*")
	if err != nil {
		return nil, fmt.Errorf("derive: temp dir: %w", err)
	}
	defer os.RemoveAll(suiteDir)
	if err := suite.WriteEvalSet(suiteDir, set); err != nil {
		return nil, fmt.Errorf("derive: write eval set: %w", err)
	}
	if err := suite.WriteTriggerQueries(suiteDir, triggers); err != nil {
		return nil, fmt.Errorf("derive: write trigger queries: %w", err)
	}

	checks := make([]evalsuite.Check, 0, len(set.Evals))
	void := map[int]bool{}
	var voidSummaries []string
	for _, c := range suite.Validate(suiteDir, set) {
		id := strconv.Itoa(c.ID)
		checks = append(checks, evalsuite.Check{TaskID: id, OK: c.OK, Void: c.Void, Reason: c.Reason})
		if c.Void {
			void[c.ID] = true
			voidSummaries = append(voidSummaries, id+": "+c.Reason)
		}
	}

	// A dry run of the real planner, not a reimplementation of its arithmetic:
	// a flat "N non-void evals" floor would pass sets BuildPlan then rejects,
	// after the set is already pushed and an eval row created.
	if _, err := runner.BuildPlan(runner.Tier(in.Tier), in.Panel, set, void, triggers); err != nil {
		d.o.Logger("derive: too thin, %d of %d evals void: %s", len(voidSummaries), len(checks), strings.Join(voidSummaries, "; "))
		return nil, fmt.Errorf("derive: the derived eval set is too thin to evaluate (%d of %d evals void: %s): %w",
			len(voidSummaries), len(checks), strings.Join(voidSummaries, "; "), err)
	}

	archive, err := evalsuite.PackDir(suiteDir)
	if err != nil {
		return nil, fmt.Errorf("derive: pack eval set: %w", err)
	}
	return &Result{Archive: archive, Checks: checks, Spec: sp}, nil
}
