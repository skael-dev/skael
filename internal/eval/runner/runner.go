package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/eval/trajectory"
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

	// Sleep is time.Sleep by default; tests override it so a backoff test does
	// not actually wait minutes.
	Sleep func(time.Duration)
	// Logger receives progress and retry notices. A no-op by default.
	Logger func(string, ...any)

	MaxRateLimitRetries int
}

// Outcome is what one planned session (skill or baseline) reported.
type Outcome struct {
	Key          store.RunKey
	VerifierExit int
	Events       []trajectory.Event
	Meta         agent.Meta
	ArtifactDir  string
	Status       string
	Err          error
}

// ProbeOutcome is what one trigger probe reported.
type ProbeOutcome struct {
	Probe    Probe
	Fired    bool
	Inferred bool
	Unknown  bool
	Err      error
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
	if err := sandbox.CheckPolicy(o.Driver, o.Untrusted); err != nil {
		return nil, err
	}
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

// healthProbePrompt is a trivial request: a member is healthy only if its
// adapter can complete a full round trip through the CLI and its own parser.
const healthProbePrompt = "Reply with the single word: ready."

// healthProbeTimeout is short on purpose — a health probe is meant to fail
// fast on a churned CLI or an expired token, not to wait out a real session.
const healthProbeTimeout = 2 * time.Minute

// ProbePanel runs one trivial session against every panel member and reports
// whether each came back healthy. A member is healthy only when both the
// invocation and the parse succeed: an adapter that invokes and cannot parse
// produces a trajectory of nothing, which would otherwise score as a session
// in which the agent did nothing.
func (r *Runner) ProbePanel(ctx context.Context, p Panel, image sandbox.ImageRef) ([]Health, error) {
	out := make([]Health, 0, len(p))
	for _, m := range p {
		out = append(out, r.probeMember(ctx, m, image))
	}
	return out, nil
}

func (r *Runner) probeMember(ctx context.Context, m Member, image sandbox.ImageRef) Health {
	a, ok := r.o.Adapters(m.Agent)
	if !ok {
		return Health{Member: m, OK: false, Detail: fmt.Sprintf("no adapter registered for %q", m.Agent)}
	}

	ws, err := stageProbeWorkspace()
	if err != nil {
		return Health{Member: m, OK: false, Detail: fmt.Sprintf("staging probe workspace: %v", err)}
	}

	mounts, err := authMounts(a.Caps().AuthDirs)
	if err != nil {
		return Health{Member: m, OK: false, Detail: err.Error()}
	}

	exec := sandbox.NewExec(r.o.Driver, sandbox.RunSpec{
		Image:     image,
		Workspace: ws,
		Mounts:    mounts,
		Network:   sandbox.NetAllowlist,
		Allow:     r.o.AllowDomains,
		Timeout:   healthProbeTimeout,
	})

	stream, err := a.Invoke(ctx, agent.InvokeSpec{
		Workspace: ws,
		Prompt:    healthProbePrompt,
		Model:     m.Model,
		Timeout:   healthProbeTimeout,
		Exec:      exec,
	})
	if err != nil {
		return Health{Member: m, OK: false, Detail: fmt.Sprintf("invoke: %v", err)}
	}
	if _, err := a.Parse(stream); err != nil {
		return Health{Member: m, OK: false, Detail: fmt.Sprintf("parse: %v", err)}
	}
	return Health{Member: m, OK: true}
}

// memberHealthy reports whether m may run. A member absent from h is treated
// as healthy — Healthy is only ever populated from a prior ProbePanel call,
// and an eval run without one (e.g. a resumed run whose caller re-probes
// separately) must not silently skip every session.
func memberHealthy(h map[Member]bool, m Member) bool {
	ok, known := h[m]
	if !known {
		return true
	}
	return ok
}

// Execute runs in.Plan against evalID, claiming each session first so a
// resumed Execute over the same eval spends nothing on work already
// recorded.
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

// findTask returns the task with id from the plan's task list.
func findTask(tasks []suite.TaskPkg, id string) (suite.TaskPkg, bool) {
	for _, t := range tasks {
		if t.ID == id {
			return t, true
		}
	}
	return suite.TaskPkg{}, false
}

// executeRun runs one skill or baseline session, claiming it first so a
// resumed Execute skips it entirely once it has finished.
func (r *Runner) executeRun(ctx context.Context, evalID int64, in ExecuteInput, byKey map[store.RunKey]store.RunRecord, k store.RunKey) Outcome {
	out := Outcome{Key: k}

	runID, done, err := r.o.Store.ClaimRun(evalID, k)
	if err != nil {
		out.Status, out.Err = store.StatusError, fmt.Errorf("runner: claiming run: %w", err)
		return out
	}
	if done {
		if rec, ok := byKey[k]; ok {
			return outcomeFromRecord(rec)
		}
		return out
	}

	artifactDir, adErr := r.o.Store.RunDir(in.Skill, evalID, k)
	if adErr == nil {
		if mkErr := os.MkdirAll(artifactDir, 0o755); mkErr != nil {
			adErr = mkErr
		}
	}

	finish := func(status string, ferr error) Outcome {
		out.Status, out.Err, out.ArtifactDir = status, ferr, artifactDir
		errStr := ""
		if ferr != nil {
			errStr = ferr.Error()
		}
		fo := store.RunOutcome{
			VerifierExit: out.VerifierExit,
			InputTokens:  out.Meta.InputTokens,
			OutputTokens: out.Meta.OutputTokens,
			DurationMS:   out.Meta.DurationMS,
			AgentVersion: out.Meta.AgentVersion,
			RateLimited:  out.Meta.RateLimited,
			Status:       status,
			Error:        errStr,
			ArtifactDir:  artifactDir,
		}
		if err := r.o.Store.FinishRun(runID, fo); err != nil {
			r.o.Logger("runner: finishing run %+v: %v", k, err)
		}
		return out
	}

	if adErr != nil {
		return finish(store.StatusError, fmt.Errorf("runner: preparing artifact dir: %w", adErr))
	}

	task, ok := findTask(in.Plan.Tasks, k.TaskID)
	if !ok {
		return finish(store.StatusError, fmt.Errorf("runner: no task %q in the plan", k.TaskID))
	}
	a, ok := r.o.Adapters(k.Agent)
	if !ok {
		return finish(store.StatusError, fmt.Errorf("runner: no adapter registered for %q", k.Agent))
	}

	taskDir := filepath.Join(in.SuiteDir, "tasks", k.TaskID)
	ws, err := stageRunWorkspace(taskDir)
	if err != nil {
		return finish(store.StatusError, err)
	}

	// The skill installs only for the skill and trigger conditions. A baseline
	// workspace carrying the skill would make Uplift measure nothing.
	if k.Condition == CondSkill || k.Condition == CondTrigger {
		if err := a.InstallSkill(ws, in.BundleDir); err != nil {
			return finish(store.StatusError, fmt.Errorf("runner: installing skill: %w", err))
		}
	}
	if k.Condition == CondTrigger {
		if err := installDistractors(ws, a.Caps().SkillDir, in.Distractors); err != nil {
			return finish(store.StatusError, fmt.Errorf("runner: installing distractors: %w", err))
		}
	}

	mounts, err := authMounts(a.Caps().AuthDirs)
	if err != nil {
		return finish(store.StatusError, err)
	}
	exec := sandbox.NewExec(r.o.Driver, sandbox.RunSpec{
		Image:     in.Image,
		Workspace: ws,
		Mounts:    mounts,
		Network:   sandbox.NetAllowlist,
		Allow:     r.o.AllowDomains,
		Timeout:   r.o.SessionTimeout,
	})

	skillName := ""
	if k.Condition == CondSkill || k.Condition == CondTrigger {
		skillName = in.Skill
	}
	result, status, err := r.invoke(ctx, a, agent.InvokeSpec{
		Workspace: ws,
		Prompt:    task.PromptMD,
		Model:     k.Model,
		Timeout:   r.o.SessionTimeout,
		SkillName: skillName,
		Exec:      exec,
	}, k)
	if err != nil {
		return finish(status, err)
	}
	out.Events, out.Meta = result.Events, result.Meta

	// The verifier runs under NetNone in the same workspace: it must not be
	// able to reach the network, or a task could be satisfied by fetching the
	// answer instead of solving it.
	vres, err := r.o.Driver.Run(ctx, sandbox.RunSpec{
		Image:     in.Image,
		Workspace: ws,
		Argv:      []string{"sh", "/verifier/test.sh"},
		Mounts:    []sandbox.Mount{{HostPath: filepath.Join(taskDir, "verifier"), ContainerPath: "/verifier", ReadOnly: true}},
		Network:   sandbox.NetNone,
		Timeout:   r.o.SessionTimeout,
	})
	if err != nil {
		return finish(store.StatusError, fmt.Errorf("runner: running verifier: %w", err))
	}
	out.VerifierExit = vres.ExitCode
	if vres.TimedOut {
		return finish(store.StatusTimeout, fmt.Errorf("runner: verifier timed out: %w", context.DeadlineExceeded))
	}
	if vres.ExitCode != 0 {
		return finish(store.StatusFailed, nil)
	}
	return finish(store.StatusOK, nil)
}

// executeProbe runs one trigger probe: a short session measuring only
// whether the skill fired, against a workspace carrying the skill and the
// distractor pack — without distractors, the skill is the only candidate and
// always "wins", which measures nothing.
func (r *Runner) executeProbe(ctx context.Context, evalID int64, in ExecuteInput, byKey map[store.RunKey]store.RunRecord, p Probe) ProbeOutcome {
	po := ProbeOutcome{Probe: p}
	k := probeKey(p)

	runID, done, err := r.o.Store.ClaimRun(evalID, k)
	if err != nil {
		po.Unknown, po.Err = true, fmt.Errorf("runner: claiming probe: %w", err)
		return po
	}
	if done {
		if rec, ok := byKey[k]; ok {
			po.Fired = rec.Outcome.VerifierExit != 0
			return po
		}
		return po
	}

	artifactDir, _ := r.o.Store.RunDir(in.Skill, evalID, k)
	if artifactDir != "" {
		_ = os.MkdirAll(artifactDir, 0o755)
	}

	finish := func(fired bool, ferr error) ProbeOutcome {
		status := store.StatusOK
		errStr := ""
		if ferr != nil {
			status, errStr = store.StatusError, ferr.Error()
		}
		verifierExit := 0
		if fired {
			verifierExit = 1
		}
		fo := store.RunOutcome{VerifierExit: verifierExit, Status: status, Error: errStr, ArtifactDir: artifactDir}
		if err := r.o.Store.FinishRun(runID, fo); err != nil {
			r.o.Logger("runner: finishing probe %+v: %v", k, err)
		}
		po.Fired, po.Err = fired, ferr
		if ferr != nil {
			po.Unknown = true
		}
		return po
	}

	a, ok := r.o.Adapters(p.Member.Agent)
	if !ok {
		return finish(false, fmt.Errorf("runner: no adapter registered for %q", p.Member.Agent))
	}

	ws, err := stageProbeWorkspace()
	if err != nil {
		return finish(false, err)
	}
	if err := a.InstallSkill(ws, in.BundleDir); err != nil {
		return finish(false, fmt.Errorf("runner: installing skill: %w", err))
	}
	if err := installDistractors(ws, a.Caps().SkillDir, in.Distractors); err != nil {
		return finish(false, fmt.Errorf("runner: installing distractors: %w", err))
	}

	mounts, err := authMounts(a.Caps().AuthDirs)
	if err != nil {
		return finish(false, err)
	}
	exec := sandbox.NewExec(r.o.Driver, sandbox.RunSpec{
		Image:     in.Image,
		Workspace: ws,
		Mounts:    mounts,
		Network:   sandbox.NetAllowlist,
		Allow:     r.o.AllowDomains,
		Timeout:   r.o.SessionTimeout,
	})

	result, _, err := r.invoke(ctx, a, agent.InvokeSpec{
		Workspace: ws,
		Prompt:    p.Prompt,
		Model:     p.Member.Model,
		Timeout:   r.o.SessionTimeout,
		SkillName: in.Skill,
		Exec:      exec,
	}, k)
	if err != nil {
		return finish(false, err)
	}

	fired := firedForSkill(result.Events, result.Meta, in.Skill)
	po.Inferred = !a.Caps().SupportsSkillInvocation
	return finish(fired, nil)
}

// probeKey gives a trigger probe a store.RunKey so it can be claimed and
// resumed the same way a task run is: the probe's index makes it unique
// among the primary member's sessions, and Condition marks it as a probe
// rather than a measured task.
func probeKey(p Probe) store.RunKey {
	return store.RunKey{
		TaskID:    fmt.Sprintf("trigger-%02d", p.Index),
		Agent:     p.Member.Agent,
		Model:     p.Member.Model,
		Condition: CondTrigger,
		Attempt:   1,
	}
}

// firedForSkill reports whether a parsed session shows the skill under test
// being read or otherwise surfaced.
func firedForSkill(events []trajectory.Event, meta agent.Meta, skill string) bool {
	for _, e := range events {
		if e.Type == trajectory.TypeSkillRead && e.Name == skill {
			return true
		}
	}
	for _, v := range meta.VisibleSkills {
		if v == skill {
			return true
		}
	}
	return false
}

// outcomeFromRecord rebuilds an Outcome from a previously finished run, for
// resume. Events are not persisted to the store (they are recorded as
// artifacts), so a resumed Outcome carries none — every caller that needs
// them reads the eval's stored artifacts directly.
func outcomeFromRecord(rec store.RunRecord) Outcome {
	var err error
	if rec.Outcome.Error != "" {
		err = errors.New(rec.Outcome.Error)
	}
	return Outcome{
		Key:          rec.Key,
		VerifierExit: rec.Outcome.VerifierExit,
		Meta: agent.Meta{
			InputTokens:  rec.Outcome.InputTokens,
			OutputTokens: rec.Outcome.OutputTokens,
			DurationMS:   rec.Outcome.DurationMS,
			AgentVersion: rec.Outcome.AgentVersion,
			RateLimited:  rec.Outcome.RateLimited,
		},
		ArtifactDir: rec.Outcome.ArtifactDir,
		Status:      rec.Outcome.Status,
		Err:         err,
	}
}

// invoke runs one agent session, retrying a rate-limited response after a
// backoff rather than recording it as a failure — a plan rate limit is a
// property of the account, not of the skill. It returns a store status only
// on a hard failure (status is "" on success).
func (r *Runner) invoke(ctx context.Context, a agent.Adapter, spec agent.InvokeSpec, k store.RunKey) (*agent.Result, string, error) {
	for attempt := 0; ; attempt++ {
		stream, err := a.Invoke(ctx, spec)
		if err != nil {
			status := store.StatusError
			if errors.Is(err, context.DeadlineExceeded) {
				status = store.StatusTimeout
			}
			return nil, status, err
		}
		result, err := a.Parse(stream)
		if err != nil {
			return nil, store.StatusError, fmt.Errorf("runner: parsing session: %w", err)
		}
		if result.Meta.RateLimited && attempt+1 < r.o.MaxRateLimitRetries {
			d := backoff(attempt)
			r.o.Logger("runner: %+v rate limited, retrying in %s (attempt %d)", k, d, attempt+1)
			r.o.Sleep(d)
			continue
		}
		return result, "", nil
	}
}

// backoff is long enough for a per-minute rate-limit window to reset and
// short enough that a sixty-session tier still finishes: it doubles from 30s
// and caps at five minutes.
func backoff(attempt int) time.Duration {
	d := time.Duration(1<<attempt) * 30 * time.Second
	const maxBackoff = 5 * time.Minute
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

// authMounts expands an adapter's declared auth directories to absolute host
// paths, mounted read-only so subscription auth works inside the sandbox
// without the run being able to modify it.
func authMounts(dirs []string) ([]sandbox.Mount, error) {
	if len(dirs) == 0 {
		return nil, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("runner: resolving home directory for auth mounts: %w", err)
	}
	mounts := make([]sandbox.Mount, 0, len(dirs))
	for _, d := range dirs {
		p := d
		switch {
		case strings.HasPrefix(p, "~/"):
			p = filepath.Join(home, p[2:])
		case !filepath.IsAbs(p):
			p = filepath.Join(home, p)
		}
		mounts = append(mounts, sandbox.Mount{HostPath: p, ContainerPath: p, ReadOnly: true})
	}
	return mounts, nil
}
