package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

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
// resumed Execute skips it entirely once it has finished. Plan.Runs never
// contains a CondTrigger key — BuildPlan puts trigger probes in Plan.Probes,
// handled by executeProbe — so this only ever sees CondSkill or CondBaseline.
func (r *Runner) executeRun(ctx context.Context, evalID int64, in ExecuteInput, byKey map[store.RunKey]store.RunRecord, k store.RunKey) Outcome {
	out := Outcome{Key: k}
	startedAt := time.Now().UTC()

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

	// ws and raw are filled in as the session progresses; finish reads
	// whatever they hold at call time, so artifacts are recorded even when
	// the run ends before invoke completes.
	var (
		ws       string
		raw      []byte
		skipDirs []string
	)

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
		if artifactDir != "" && ws != "" {
			g := Grading{
				Key: k, VerifierExit: out.VerifierExit, Meta: out.Meta, Status: status, Error: errStr,
				StartedAt: startedAt, FinishedAt: time.Now().UTC(),
			}
			if _, wErr := WriteArtifacts(artifactDir, raw, out.Events, g, ws, skipDirs); wErr != nil {
				r.o.Logger("runner: writing artifacts for %+v: %v", k, wErr)
			}
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
	ws, err = stageRunWorkspace(taskDir)
	if err != nil {
		return finish(store.StatusError, err)
	}
	defer func() {
		if rmErr := os.RemoveAll(ws); rmErr != nil {
			r.o.Logger("runner: removing workspace %s: %v", ws, rmErr)
		}
	}()

	// The skill installs only for the skill condition. A baseline workspace
	// carrying the skill would make Uplift measure nothing. skipDirs mirrors
	// that: a baseline has no installed skill to exclude from outputs/, so
	// its real outputs are copied in full.
	if k.Condition == CondSkill {
		if err := a.InstallSkill(ws, in.BundleDir); err != nil {
			return finish(store.StatusError, fmt.Errorf("runner: installing skill: %w", err))
		}
		skipDirs = []string{a.Caps().SkillDir}
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
	if k.Condition == CondSkill {
		skillName = in.Skill
	}
	result, invokeRaw, status, err := r.invoke(ctx, a, agent.InvokeSpec{
		Workspace: ws,
		Prompt:    task.PromptMD,
		Model:     k.Model,
		Timeout:   r.o.SessionTimeout,
		SkillName: skillName,
		Exec:      exec,
	}, k)
	raw = invokeRaw
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

// executeProbe runs one trigger probe: a short session against a workspace
// carrying the skill and the distractor pack — without distractors, the
// skill is the only candidate and always "wins", which measures nothing. The
// runner only observes the session: it records the trajectory, meta, and
// adapter capabilities, and leaves the firing determination to scoring.
func (r *Runner) executeProbe(ctx context.Context, evalID int64, in ExecuteInput, byKey map[store.RunKey]store.RunRecord, p Probe) ProbeOutcome {
	po := ProbeOutcome{Probe: p}
	k := probeKey(p)
	startedAt := time.Now().UTC()

	runID, done, err := r.o.Store.ClaimRun(evalID, k)
	if err != nil {
		po.Unknown, po.Reason, po.Err = true, "claiming probe failed", fmt.Errorf("runner: claiming probe: %w", err)
		return po
	}
	if done {
		if rec, ok := byKey[k]; ok {
			return resumeProbeOutcome(r.o.Adapters, p, rec)
		}
		return ProbeOutcome{Probe: p, Unknown: true, Reason: "resumed probe: no record found for a claimed run"}
	}

	artifactDir, adErr := r.o.Store.RunDir(in.Skill, evalID, k)
	if adErr == nil {
		if mkErr := os.MkdirAll(artifactDir, 0o755); mkErr != nil {
			adErr = mkErr
		}
	}

	// ws, raw, and a are filled in as the probe progresses; finish reads
	// whatever they hold at call time, so artifacts are recorded even when
	// the probe ends before invoke completes.
	var (
		ws  string
		raw []byte
		a   agent.Adapter
	)

	finish := func(ferr error) ProbeOutcome {
		status := store.StatusOK
		errStr := ""
		if ferr != nil {
			status, errStr = store.StatusError, ferr.Error()
		}
		fo := store.RunOutcome{
			InputTokens:  po.Meta.InputTokens,
			OutputTokens: po.Meta.OutputTokens,
			DurationMS:   po.Meta.DurationMS,
			AgentVersion: po.Meta.AgentVersion,
			RateLimited:  po.Meta.RateLimited,
			Status:       status,
			Error:        errStr,
			ArtifactDir:  artifactDir,
		}
		if err := r.o.Store.FinishRun(runID, fo); err != nil {
			r.o.Logger("runner: finishing probe %+v: %v", k, err)
		}
		if artifactDir != "" && ws != "" {
			g := Grading{
				Key: k, Meta: po.Meta, Status: status, Error: errStr,
				StartedAt: startedAt, FinishedAt: time.Now().UTC(),
			}
			// A probe always installs the skill and, when configured, the
			// distractor pack alongside it — both live under the adapter's
			// SkillDir, so excluding it keeps every copy of the bundle out
			// of outputs/ regardless of which distractors were installed.
			skipDirs := []string{a.Caps().SkillDir}
			if _, wErr := WriteArtifacts(artifactDir, raw, po.Events, g, ws, skipDirs); wErr != nil {
				r.o.Logger("runner: writing artifacts for probe %+v: %v", k, wErr)
			}
		}
		po.Err = ferr
		if ferr != nil {
			po.Unknown, po.Reason = true, ferr.Error()
		}
		return po
	}

	if adErr != nil {
		return finish(fmt.Errorf("runner: preparing artifact dir: %w", adErr))
	}

	var ok bool
	a, ok = r.o.Adapters(p.Member.Agent)
	if !ok {
		return finish(fmt.Errorf("runner: no adapter registered for %q", p.Member.Agent))
	}

	ws, err = stageProbeWorkspace()
	if err != nil {
		return finish(err)
	}
	defer func() {
		if rmErr := os.RemoveAll(ws); rmErr != nil {
			r.o.Logger("runner: removing probe workspace %s: %v", ws, rmErr)
		}
	}()

	if err := a.InstallSkill(ws, in.BundleDir); err != nil {
		return finish(fmt.Errorf("runner: installing skill: %w", err))
	}
	if err := installDistractors(ws, a.Caps().SkillDir, in.Distractors); err != nil {
		return finish(fmt.Errorf("runner: installing distractors: %w", err))
	}

	mounts, err := authMounts(a.Caps().AuthDirs)
	if err != nil {
		return finish(err)
	}
	exec := sandbox.NewExec(r.o.Driver, sandbox.RunSpec{
		Image:     in.Image,
		Workspace: ws,
		Mounts:    mounts,
		Network:   sandbox.NetAllowlist,
		Allow:     r.o.AllowDomains,
		Timeout:   r.o.SessionTimeout,
	})

	result, invokeRaw, _, err := r.invoke(ctx, a, agent.InvokeSpec{
		Workspace: ws,
		Prompt:    p.Prompt,
		Model:     p.Member.Model,
		Timeout:   r.o.SessionTimeout,
		SkillName: in.Skill,
		Exec:      exec,
	}, k)
	raw = invokeRaw
	if err != nil {
		return finish(err)
	}

	po.Events, po.Meta, po.Caps = result.Events, result.Meta, a.Caps()
	return finish(nil)
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

// resumeProbeOutcome rebuilds a ProbeOutcome from a previously finished probe
// run. Unlike a task run, a probe's evidence is entirely in its trajectory,
// which is not stored in the database — so resume reloads events.jsonl from
// the recorded artifact directory when it exists. Artifact writing is a
// later task, so today that file never exists yet; Unknown is the honest
// answer until it does, and an unknown probe is excluded from trigger
// denominators rather than counted as a miss.
func resumeProbeOutcome(adapters func(string) (agent.Adapter, bool), p Probe, rec store.RunRecord) ProbeOutcome {
	po := ProbeOutcome{Probe: p}
	if a, ok := adapters(p.Member.Agent); ok {
		po.Caps = a.Caps()
	}
	po.Meta = agent.Meta{
		InputTokens:  rec.Outcome.InputTokens,
		OutputTokens: rec.Outcome.OutputTokens,
		DurationMS:   rec.Outcome.DurationMS,
		AgentVersion: rec.Outcome.AgentVersion,
		RateLimited:  rec.Outcome.RateLimited,
	}
	if rec.Outcome.Error != "" {
		po.Err = errors.New(rec.Outcome.Error)
	}

	events, err := loadProbeEvents(rec.Outcome.ArtifactDir)
	if err != nil {
		po.Unknown = true
		po.Reason = fmt.Sprintf("resumed probe: recovering events from %q: %v", rec.Outcome.ArtifactDir, err)
		return po
	}
	po.Events = events
	return po
}

// loadProbeEvents reads a newline-delimited JSON trajectory from
// <artifactDir>/events.jsonl.
func loadProbeEvents(artifactDir string) ([]trajectory.Event, error) {
	if artifactDir == "" {
		return nil, errors.New("no artifact directory recorded")
	}
	f, err := os.Open(filepath.Join(artifactDir, "events.jsonl"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var events []trajectory.Event
	dec := json.NewDecoder(f)
	for dec.More() {
		var e trajectory.Event
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("decoding event: %w", err)
		}
		events = append(events, e)
	}
	return events, nil
}

// invoke runs one agent session, retrying a rate-limited response after a
// backoff rather than recording it as a failure — a plan rate limit is a
// property of the account, not of the skill. Once the retry budget is
// exhausted while still rate-limited, or the agent itself reports an
// internal error (Meta.IsError), the session could not be performed at all:
// it is reported as a hard failure so ClaimRun retries it on the next
// Execute, rather than permanently recording a rate-limit artifact as if it
// were a skill failure. The returned status is empty on success.
// invoke returns the parsed result alongside the raw bytes of the native
// stream that produced it (for the attempt actually returned — a discarded,
// rate-limited attempt's bytes are not kept), so a caller can record both.
func (r *Runner) invoke(ctx context.Context, a agent.Adapter, spec agent.InvokeSpec, k store.RunKey) (*agent.Result, []byte, string, error) {
	for attempt := 0; ; attempt++ {
		stream, err := a.Invoke(ctx, spec)
		if err != nil {
			status := store.StatusError
			if errors.Is(err, context.DeadlineExceeded) {
				status = store.StatusTimeout
			}
			return nil, nil, status, err
		}
		raw, err := io.ReadAll(stream)
		if err != nil {
			return nil, nil, store.StatusError, fmt.Errorf("runner: reading session stream: %w", err)
		}
		result, err := a.Parse(bytes.NewReader(raw))
		if err != nil {
			return nil, raw, store.StatusError, fmt.Errorf("runner: parsing session: %w", err)
		}

		if result.Meta.RateLimited {
			if attempt+1 < r.o.MaxRateLimitRetries {
				d := backoff(attempt)
				r.o.Logger("runner: %+v rate limited, retrying in %s (attempt %d)", k, d, attempt+1)
				r.o.Sleep(d)
				continue
			}
			return nil, raw, store.StatusError, fmt.Errorf("runner: %+v still rate limited after %d attempts", k, r.o.MaxRateLimitRetries)
		}
		if result.Meta.IsError {
			return nil, raw, store.StatusError, fmt.Errorf("runner: %+v agent reported an internal error", k)
		}
		return result, raw, "", nil
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
