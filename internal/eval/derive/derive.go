// Package derive builds an eval suite for a skill that has none, by
// recovering a spec from the bundle and drafting evals from it.
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

// evalCount overbudgets so voided evals still leave enough for BuildPlan.
const evalCount = 14

// Options are the deriver's injected dependencies.
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

// Derive recovers a spec from the bundle, drafts an eval set, and validates
// it statically.
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

	// Dry-run the real planner to reject thin suites before they are pushed.
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
