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

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalsuite"
	skillpkg "github.com/skael-dev/skael/internal/skill"
)

// taskCount is how many task packages a derived suite asks for. More than the
// authored path's 10: there is no author to repair a task the oracle gate
// voids, so the surplus is what lets runner.BuildPlan still plan afterwards. A
// 0.3 split of 18 leaves roughly 5 holdout and 13 dev, and tier "full" needs 3
// and 7.
const taskCount = 18

// splitSeed matches cli/whetstone's constant. The holdout is what a reported
// score means, so the split must not vary between the two paths.
const splitSeed int64 = 1

// Options are the deriver's injected dependencies.
type Options struct {
	Gateway llm.Gateway
	Driver  sandbox.Driver
	// BaseTag selects the sandbox base image; empty means the default.
	BaseTag string
	// StageRoot is where the oracle gate stages task workspaces. It must be a
	// path bind-mounted identically for this process and for the Docker
	// daemon — see suite.CheckOptions.StageRoot.
	StageRoot string
	Logger    func(format string, args ...any)
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
	if o.Driver == nil {
		return nil, errors.New("derive: New requires a non-nil Driver")
	}
	if o.Logger == nil {
		o.Logger = func(string, ...any) {}
	}
	return &Deriver{o: o}, nil
}

// Derive recovers a spec from the bundle, drafts a suite, gates it against its
// own oracles, and packs it.
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

	// Drops are discarded here; Task 3 wires them into the derive result.
	s, _, err := suite.GenerateN(ctx, d.o.Gateway, sp, taskCount)
	if err != nil {
		return nil, fmt.Errorf("derive: generate suite: %w", err)
	}
	s.Split(splitSeed)

	suiteDir, err := os.MkdirTemp("", "derive-suite-*")
	if err != nil {
		return nil, fmt.Errorf("derive: temp dir: %w", err)
	}
	defer os.RemoveAll(suiteDir)
	if err := s.Write(suiteDir); err != nil {
		return nil, fmt.Errorf("derive: write suite: %w", err)
	}

	checks, err := d.gate(ctx, s, sp, suiteDir)
	if err != nil {
		return nil, err
	}

	void := make(map[string]bool, len(checks))
	for _, c := range checks {
		if c.Void {
			void[c.TaskID] = true
		}
	}
	// A dry run of the real planner, not a reimplementation of its arithmetic:
	// a flat "N non-void tasks" floor would pass suites BuildPlan then rejects,
	// after the suite is already pushed. Its message names the failing split.
	if _, err := runner.BuildPlan(runner.Tier(in.Tier), in.Panel, s, void); err != nil {
		return nil, fmt.Errorf("derive: the derived suite is too thin to evaluate: %w", err)
	}

	archive, err := evalsuite.PackDir(suiteDir)
	if err != nil {
		return nil, fmt.Errorf("derive: pack suite: %w", err)
	}
	return &Result{Archive: archive, Checks: checks, Spec: sp}, nil
}

// gate runs the oracle gate and converts its results to registry checks.
func (d *Deriver) gate(ctx context.Context, s *suite.Suite, sp *spec.SkillSpec, suiteDir string) ([]evalsuite.Check, error) {
	// untrusted:false matches runner.New's default and every other
	// docker-driver caller. The oracle and verifier scripts here are
	// model-generated from a third party's SKILL.md, which is the same trust
	// level the panel already runs that skill's own instructions at; passing
	// true would refuse outright, because Docker shares the host kernel.
	gd, err := sandbox.Gated(d.o.Driver, false)
	if err != nil {
		return nil, fmt.Errorf("derive: %w", err)
	}

	image, err := d.prepare(ctx, sp)
	if err != nil {
		return nil, err
	}

	results, err := suite.Check(ctx, s, suite.CheckOptions{
		Driver: gd, Image: image, SuiteDir: suiteDir,
		Timeout: suite.VerifierTimeout, StageRoot: d.o.StageRoot,
		Concurrency: 4, Logger: d.o.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("derive: oracle gate: %w", err)
	}

	checks := make([]evalsuite.Check, 0, len(s.Tasks))
	for _, r := range results {
		checks = append(checks, evalsuite.Check{
			TaskID: r.TaskID, OK: !r.Void, Void: r.Void, Reason: r.Reason,
		})
	}
	return checks, nil
}

// imagePreparer is the part of the Docker driver the oracle gate needs beyond
// Run. A driver that does not implement it — a test fake — gets a zero
// ImageRef and no base image, which is all a fake needs.
type imagePreparer interface {
	Sweep(ctx context.Context)
	EnsureBase(ctx context.Context, slim bool) error
	Prepare(ctx context.Context, e sandbox.EnvSpec) (sandbox.ImageRef, error)
}

func (d *Deriver) prepare(ctx context.Context, sp *spec.SkillSpec) (sandbox.ImageRef, error) {
	p, ok := d.o.Driver.(imagePreparer)
	if !ok {
		return sandbox.ImageRef{}, nil
	}
	// A prior run killed by something stronger than its own context can leave
	// containers and networks behind; sweeping first keeps those from
	// exhausting the docker address pool on a long-lived worker.
	p.Sweep(ctx)
	if err := p.EnsureBase(ctx, d.o.BaseTag == imagespec.SlimBaseTag); err != nil {
		return sandbox.ImageRef{}, fmt.Errorf("derive: preparing base image: %w", err)
	}
	img, err := p.Prepare(ctx, sandbox.EnvSpec{Skill: sp.Name, Deps: sp.Deps, BaseTag: d.o.BaseTag})
	if err != nil {
		return sandbox.ImageRef{}, fmt.Errorf("derive: preparing image: %w", err)
	}
	return img, nil
}
