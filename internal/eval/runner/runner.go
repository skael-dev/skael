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

	// WorkspaceRoot is the directory session workspaces are created under.
	// Empty means os.TempDir(), which is correct for every run whose sandbox
	// containers are started by the same machine's Docker daemon.
	//
	// It exists for the one case where that is not true: a runner inside a
	// container, talking to the host daemon over a mounted socket. A
	// workspace is bind-mounted into each sandbox, and the daemon resolves
	// that path in the *host's* filesystem — so a path under the runner's own
	// /tmp names nothing on the host, and Docker silently creates an empty
	// directory rather than failing. The sandbox then comes up with no task,
	// which scores as a skill that did nothing. Setting this to a path that
	// is bind-mounted at the *same* location on both sides makes the path
	// mean the same thing to both, which is the whole requirement.
	WorkspaceRoot string

	// Sleep is time.Sleep by default; tests override it so a backoff test does
	// not actually wait minutes.
	Sleep func(time.Duration)
	// Logger receives progress and retry notices. A no-op by default.
	Logger func(string, ...any)

	MaxRateLimitRetries int
}

// Outcome is what one planned session (skill or baseline) reported.
type Outcome struct {
	Key         store.RunKey
	Events      []agent.Event
	Meta        agent.Meta
	ArtifactDir string
	Status      string
	Err         error
	// MetaPartial is true when Meta was rebuilt from the store's own columns
	// rather than from the run's grading.json artifact — on resume, when the
	// artifact is missing or unreadable. A partial Meta carries only what
	// those columns hold (tokens, duration, agent version, rate-limited);
	// Model, NumTurns, VisibleSkills, PermissionDenials, and IsError are
	// silently zero, so a caller that feeds Meta into anything reading those
	// fields (VisibleSkills feeds trigger precision) must check this first.
	MetaPartial       bool
	MetaPartialReason string
}

// ProbeOutcome is what one trigger probe observed. It deliberately carries no
// pre-computed verdict: whether the skill fired, and whether that
// determination was explicit or inferred, depends on the adapter's
// capabilities and is scoring's job, not the runner's — score.DetectFiring
// (a later task) is the one definition of firing, working from exactly these
// fields.
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
	Outcomes []Outcome
	Probes   []ProbeOutcome
	// PanelComplete is false when any planned panel member was skipped for
	// being unhealthy. A false PanelComplete means the eval's scores are
	// partial, not that the skill scored zero on that member.
	PanelComplete bool
}

// Runner executes a plan.
type Runner struct{ o Options }

// New validates the options and applies defaults: concurrency 4 (§9's
// scheduling assumption), a 20-minute session timeout, and three rate-limit
// retries.
func New(o Options) (*Runner, error) {
	if o.Store == nil || o.Driver == nil || o.Adapters == nil {
		return nil, errors.New("runner: store, driver, and adapters are required")
	}
	// Gated makes the trust decision structural rather than a check every
	// caller of o.Driver.Run must remember to make: it refuses here for
	// untrusted work on a shared-kernel driver, and for untrusted work on a
	// hardware-isolated driver it wraps Run so the same refusal still holds
	// even if o.Driver is later read out of a struct field.
	gd, err := sandbox.Gated(o.Driver, o.Untrusted)
	if err != nil {
		return nil, err
	}
	o.Driver = gd
	if o.Concurrency <= 0 {
		o.Concurrency = 4
	}
	if o.SessionTimeout <= 0 {
		o.SessionTimeout = 20 * time.Minute
	}
	if o.MaxRateLimitRetries == 0 {
		o.MaxRateLimitRetries = 3
	}
	if len(o.AllowDomains) == 0 {
		o.AllowDomains = DefaultAllowDomains
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.Logger == nil {
		o.Logger = func(string, ...any) {}
	}
	return &Runner{o: o}, nil
}

// DefaultAllowDomains is the egress an agent session needs and nothing more.
// A session with no network cannot authenticate; a session with full network
// makes "did the skill reach out" unanswerable.
var DefaultAllowDomains = []string{"api.anthropic.com", "statsig.anthropic.com", "sentry.io"}

// Execute runs in.Plan against evalID, claiming each session first so a
// resumed Execute over the same eval spends nothing on work already
// recorded.
func (r *Runner) Execute(ctx context.Context, evalID int64, in ExecuteInput) (*ExecuteResult, error) {
	if in.Plan == nil {
		return nil, errors.New("runner: Execute needs a plan")
	}

	panelComplete := true
	for _, m := range in.Plan.Panel {
		// This walks the full configured panel, not just the members a given
		// tier actually schedules (a Smoke tier's PrimaryOnly budget never
		// touches the floor member, for instance). An unhealthy member the
		// tier never runs still makes the panel incomplete: PanelComplete
		// must never overclaim completeness.
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
			o := r.executeRun(ctx, evalID, in, byKey, k)
			mu.Lock()
			outcomes = append(outcomes, o)
			mu.Unlock()
		}(k)
	}
	wg.Wait()

	for _, p := range in.Plan.Probes {
		if !memberHealthy(in.Healthy, p.Member) {
			continue
		}
		wg.Add(1)
		go func(p Probe) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			po := r.executeProbe(ctx, evalID, in, byKey, p)
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
