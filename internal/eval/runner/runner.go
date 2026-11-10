package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// Options configures a Runner.
type Options struct {
	Store    *store.Store
	Driver   sandbox.Driver
	Adapters func(name string) (agent.Adapter, bool)

	Concurrency    int
	SessionTimeout time.Duration
	Untrusted      bool
	AllowDomains   []string

	// MemberAuth gives one member the environment its session needs: the
	// harness config and the provider key. A member it does not know cannot
	// run, because the sandbox inherits nothing from this process.
	MemberAuth func(agent, model string) (env []string, ok bool)

	// WorkspaceRoot overrides os.TempDir() for a containerized runner whose
	// /tmp is invisible to the host daemon. Must be bind-mounted at the same
	// path on both sides or Docker silently mounts an empty directory.
	WorkspaceRoot string

	Sleep  func(time.Duration)
	Logger func(string, ...any)

	// OnComplete, when set, is called once per finished session, from that
	// session's own goroutine. It lets a caller grade a session while the
	// rest still run: a grade needs that session's artifacts and nothing
	// else. It is never called for a probe.
	OnComplete func(Outcome)

	// OnStart, when set, is called as each planned session or probe starts,
	// and OnFinish as each one ends, with its status and error.
	OnStart  func(store.RunKey)
	OnFinish func(k store.RunKey, m agent.Meta, status string, err error)
}

// Outcome is what one planned session (skill or baseline) reported.
type Outcome struct {
	Key         store.RunKey
	Events      []agent.Event
	Meta        agent.Meta
	ArtifactDir string
	Status      string
	Err         error
	// MetaPartial is true when Meta was rebuilt from store columns rather than
	// the grading.json artifact. Model, NumTurns, VisibleSkills,
	// PermissionDenials, and IsError are zero in that case.
	MetaPartial       bool
	MetaPartialReason string
}

// ProbeOutcome is what one trigger probe observed. No pre-computed verdict:
// whether the skill fired is scoring's job (score.DetectFiring).
type ProbeOutcome struct {
	Probe  Probe
	Events []agent.Event
	Meta   agent.Meta
	Caps   agent.Caps
	// Unknown is true when the probe could not be observed at all — the
	// session failed outright, or (on resume) its events could not be
	// recovered. An unknown probe is excluded from trigger-precision
	// denominators rather than counted as a miss.
	Unknown bool
	Reason  string
	Err     error
	// MetaPartial mirrors Outcome.MetaPartial: true when Meta came from the
	// store's own columns on resume rather than the probe's grading.json.
	MetaPartial       bool
	MetaPartialReason string
}

// Health is one panel member's health-probe result.
type Health struct {
	Member Member
	OK     bool
	Detail string
}

// ExecuteInput is everything Execute needs beyond the eval id.
type ExecuteInput struct {
	Skill       string
	BundleDir   string
	SuiteDir    string
	Image       sandbox.ImageRef
	Plan        *Plan
	Healthy     map[Member]bool
	Distractors []suite.Distractor
}

// ExecuteResult is everything Execute produced.
type ExecuteResult struct {
	Outcomes      []Outcome
	Probes        []ProbeOutcome
	PanelComplete bool
}

// Runner executes a plan.
type Runner struct {
	o Options
}

// New validates options and applies defaults.
func New(o Options) (*Runner, error) {
	if o.Store == nil || o.Driver == nil || o.Adapters == nil {
		return nil, errors.New("runner: store, driver, and adapters are required")
	}
	gd, err := sandbox.Gated(o.Driver, o.Untrusted)
	if err != nil {
		return nil, err
	}
	o.Driver = gd
	if o.Concurrency <= 0 {
		o.Concurrency = 6
	}
	if o.SessionTimeout <= 0 {
		o.SessionTimeout = 20 * time.Minute
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.Logger == nil {
		o.Logger = func(string, ...any) {}
	}
	return &Runner{o: o}, nil
}

// SetOnComplete installs the per-session callback after construction. The
// runner is built before the plan is (it health-probes the panel first), and
// the caller's grade pool is sized from the plan, so the callback cannot be
// known at New time.
func (r *Runner) SetOnComplete(fn func(Outcome)) { r.o.OnComplete = fn }

// SetOnSession installs the per-session start and end callbacks after
// construction.
func (r *Runner) SetOnSession(start func(store.RunKey), finish func(store.RunKey, agent.Meta, string, error)) {
	r.o.OnStart, r.o.OnFinish = start, finish
}

func (r *Runner) started(k store.RunKey) {
	if r.o.OnStart != nil {
		r.o.OnStart(k)
	}
}

// Execute runs in.Plan, skipping sessions already recorded.
func (r *Runner) Execute(ctx context.Context, evalID int64, in ExecuteInput) (*ExecuteResult, error) {
	if in.Plan == nil {
		return nil, errors.New("runner: Execute needs a plan")
	}

	panelComplete := true
	for _, m := range in.Plan.Panel {
		if !memberHealthy(in.Healthy, m) {
			panelComplete = false
		}
	}

	existing, err := r.o.Store.Runs(evalID)
	if err != nil {
		return nil, fmt.Errorf("runner: loading existing runs: %w", err)
	}
	byKey := make(map[store.RunKey]store.RunRecord, len(existing))
	for _, rec := range existing {
		byKey[rec.Key] = rec
	}

	sem := make(chan struct{}, r.o.Concurrency)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		outcomes []Outcome
		probes   []ProbeOutcome
	)

	for _, k := range in.Plan.Runs {
		m, ok := memberFor(in.Plan.Panel, k.Agent, k.Model)
		if !ok || !memberHealthy(in.Healthy, m) {
			continue
		}
		wg.Add(1)
		go func(k store.RunKey) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			r.started(k)
			o := r.executeRun(ctx, evalID, in, byKey, k)
			if r.o.OnFinish != nil {
				r.o.OnFinish(k, o.Meta, o.Status, o.Err)
			}
			mu.Lock()
			outcomes = append(outcomes, o)
			mu.Unlock()
			// Grading one session needs that session's artifacts and nothing
			// else, so the caller can start it while the rest still run.
			if r.o.OnComplete != nil {
				r.o.OnComplete(o)
			}
		}(k)
	}

	// Probes queue against the same semaphore as the runs. Waiting for every
	// run first left most of the pool idle at the tail of a run, because a
	// probe is a short session.
	for _, p := range in.Plan.Probes {
		if !memberHealthy(in.Healthy, p.Member) {
			continue
		}
		wg.Add(1)
		go func(p Probe) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			r.started(probeKey(p))
			po := r.executeProbe(ctx, evalID, in, byKey, p)
			if r.o.OnFinish != nil {
				status := store.StatusOK
				if po.Err != nil {
					status = store.StatusError
				}
				r.o.OnFinish(probeKey(p), po.Meta, status, po.Err)
			}
			mu.Lock()
			probes = append(probes, po)
			mu.Unlock()
		}(p)
	}
	wg.Wait()

	return &ExecuteResult{Outcomes: outcomes, Probes: probes, PanelComplete: panelComplete}, nil
}

// memberFor resolves a RunKey's agent/model pair to the panel Member it came
// from, so it can be checked against in.Healthy — RunKey has no Class field,
// and Healthy is keyed on the full Member.
func memberFor(p Panel, agentName, model string) (Member, bool) {
	for _, m := range p {
		if m.Agent == agentName && m.Model == model {
			return m, true
		}
	}
	return Member{}, false
}
