package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

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
		errStr := ""
		if ferr != nil {
			errStr = ferr.Error()
		}

		if artifactDir != "" && ws != "" {
			g := Grading{
				Key: k, Meta: out.Meta, Status: status, Error: errStr,
				StartedAt: startedAt, FinishedAt: time.Now().UTC(),
			}
			if _, wErr := WriteArtifacts(artifactDir, raw, out.Events, g, ws, skipDirs); wErr != nil {
				r.o.Logger("runner: writing artifacts for %+v: %v", k, wErr)
				// Events are the only artifact scoring and resume read back;
				// losing them must not be recorded as a completed
				// measurement (StatusOK/StatusFailed), or a later resume
				// silently degrades to Unknown with nothing durable to show
				// why. A run already recorded as an error or timeout stays
				// that way — it is retried regardless, and overwriting its
				// original error would lose the reason it actually failed.
				if errors.Is(wErr, ErrEventsNotWritten) && (status == store.StatusOK || status == store.StatusFailed) {
					status = store.StatusError
					ferr = fmt.Errorf("runner: recording trajectory for %+v: %w", k, wErr)
					errStr = ferr.Error()
				}
			}
		}

		out.Status, out.Err, out.ArtifactDir = status, ferr, artifactDir
		fo := store.RunOutcome{
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

	ev, ok := in.Plan.EvalByID(k.TaskID)
	if !ok {
		return finish(store.StatusError, fmt.Errorf("runner: no eval %q in the plan", k.TaskID))
	}
	a, ok := r.o.Adapters(k.Agent)
	if !ok {
		return finish(store.StatusError, fmt.Errorf("runner: no adapter registered for %q", k.Agent))
	}

	// An eval's input files are copied on the host rather than created by a
	// script inside the sandbox. A missing file is the eval's defect, not the
	// skill's, so it ends the session as an error rather than as a failed
	// measurement that would be scored against the skill.
	ws, err = stageEvalWorkspace(in.SuiteDir, ev, r.o.WorkspaceRoot)
	if err != nil {
		return finish(store.StatusError, err)
	}
	defer func() {
		if rmErr := os.RemoveAll(ws); rmErr != nil {
			r.o.Logger("runner: removing workspace %s: %v", ws, rmErr)
		}
	}()

	// The skill installs only for the skill condition. A baseline workspace
	// carrying the skill would make the delta measure nothing. skipDirs
	// mirrors that: a baseline has no installed skill to exclude from
	// outputs/, so its real outputs are copied in full.
	if k.Condition == CondSkill {
		if err := a.InstallSkill(ws, in.BundleDir); err != nil {
			return finish(store.StatusError, fmt.Errorf("runner: installing skill: %w", err))
		}
		skipDirs = []string{a.Caps().SkillDir}
	}

	mounts, authVars, err := resolveAuth(a, r.o.Logger)
	if err != nil {
		return finish(store.StatusError, err)
	}
	warnIfNoAuth(a, mounts, r.o.Logger)
	exec := sandbox.NewExec(r.o.Driver, sandbox.RunSpec{
		Image:     in.Image,
		Workspace: ws,
		Mounts:    mounts,
		Env:       authVars,
		Network:   sandbox.NetAllowlist,
		Allow:     allowWith(r.o.AllowDomains, gatewayHosts(authVars)),
		Timeout:   r.o.SessionTimeout,
	})

	result, invokeRaw, status, err := r.invoke(ctx, a, agent.InvokeSpec{
		Prompt: ev.Prompt,
		Model:  k.Model,
		Exec:   exec,
	}, k)
	raw = invokeRaw
	if err != nil {
		return finish(status, err)
	}
	out.Events, out.Meta = result.Events, result.Meta

	// Whether the session did the task is decided later, by the grader reading
	// the transcript and the outputs this call just wrote. The runner records;
	// it does not judge.
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
				// Same rule as executeRun: a probe's events are the only
				// evidence resumeProbeOutcome can recover, so losing them
				// must not be recorded as a completed measurement.
				if errors.Is(wErr, ErrEventsNotWritten) && status == store.StatusOK {
					status = store.StatusError
					ferr = fmt.Errorf("runner: recording trajectory for probe %+v: %w", k, wErr)
					errStr = ferr.Error()
				}
			}
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

	mounts, authVars, err := resolveAuth(a, r.o.Logger)
	if err != nil {
		return finish(err)
	}
	warnIfNoAuth(a, mounts, r.o.Logger)
	exec := sandbox.NewExec(r.o.Driver, sandbox.RunSpec{
		Image:     in.Image,
		Workspace: ws,
		Mounts:    mounts,
		Env:       authVars,
		Network:   sandbox.NetAllowlist,
		Allow:     allowWith(r.o.AllowDomains, gatewayHosts(authVars)),
		Timeout:   r.o.SessionTimeout,
	})

	result, invokeRaw, _, err := r.invoke(ctx, a, agent.InvokeSpec{
		Prompt: p.Prompt,
		Model:  p.Member.Model,
		Exec:   exec,
	}, k)
	raw = invokeRaw
	if err != nil {
		return finish(err)
	}

	// Relativised as above. Trigger measurement reads only the skill directory
	// out of a path, so it survived absolute paths where drift did not — but
	// the two must not disagree about what a recorded path means.
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
//
// Meta is reloaded from the run's grading.json artifact, which is where all
// ten of its fields survive; the store's own columns cover only five. When
// the artifact is missing or unreadable (an older eval, or a lost artifact
// directory), Meta falls back to those five columns and MetaPartial records
// that the rest — Model, NumTurns, VisibleSkills, PermissionDenials,
// IsError — are not really zero, just unavailable here.
func outcomeFromRecord(rec store.RunRecord) Outcome {
	var err error
	if rec.Outcome.Error != "" {
		err = errors.New(rec.Outcome.Error)
	}
	out := Outcome{
		Key:         rec.Key,
		ArtifactDir: rec.Outcome.ArtifactDir,
		Status:      rec.Outcome.Status,
		Err:         err,
	}

	// filepath.Join("", gradingFileName) resolves to the bare relative name,
	// read against the process's cwd rather than failing — guard against
	// that rather than let an unset ArtifactDir accidentally pick up a
	// stray grading.json in whatever directory whetstone eval was run from.
	if rec.Outcome.ArtifactDir != "" {
		if g, gErr := LoadGrading(filepath.Join(rec.Outcome.ArtifactDir, gradingFileName)); gErr == nil {
			out.Meta = g.Meta
			return out
		}
	}

	out.Meta = agent.Meta{
		InputTokens:  rec.Outcome.InputTokens,
		OutputTokens: rec.Outcome.OutputTokens,
		DurationMS:   rec.Outcome.DurationMS,
		AgentVersion: rec.Outcome.AgentVersion,
		RateLimited:  rec.Outcome.RateLimited,
	}
	out.MetaPartial = true
	out.MetaPartialReason = fmt.Sprintf("resumed run: recovering full meta from %q failed; only store columns are available", rec.Outcome.ArtifactDir)
	return out
}

// resumeProbeOutcome rebuilds a ProbeOutcome from a previously finished probe
// run. Unlike a task run, a probe's evidence is entirely in its trajectory,
// which is not stored in the database — so resume reloads events.jsonl from
// the recorded artifact directory when it exists. Artifact writing is a
// later task, so today that file never exists yet; Unknown is the honest
// answer until it does, and an unknown probe is excluded from trigger
// denominators rather than counted as a miss.
//
// Meta is reloaded the same way outcomeFromRecord reloads it: from
// grading.json when available, falling back to the store's five columns —
// and marking MetaPartial — when it is not. This is independent of Unknown:
// a probe whose events recovered fine can still have a partial Meta, and the
// two must not be conflated into one all-or-nothing signal.
func resumeProbeOutcome(adapters func(string) (agent.Adapter, bool), p Probe, rec store.RunRecord) ProbeOutcome {
	po := ProbeOutcome{Probe: p}
	if a, ok := adapters(p.Member.Agent); ok {
		po.Caps = a.Caps()
	}
	if meta, mErr := loadArtifactMeta(rec.Outcome.ArtifactDir); mErr == nil {
		po.Meta = meta
	} else {
		po.Meta = agent.Meta{
			InputTokens:  rec.Outcome.InputTokens,
			OutputTokens: rec.Outcome.OutputTokens,
			DurationMS:   rec.Outcome.DurationMS,
			AgentVersion: rec.Outcome.AgentVersion,
			RateLimited:  rec.Outcome.RateLimited,
		}
		po.MetaPartial = true
		po.MetaPartialReason = fmt.Sprintf("resumed probe: recovering full meta from %q failed; only store columns are available", rec.Outcome.ArtifactDir)
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

// resolveAuth decides how a session authenticates. Environment variables win:
// when any of the adapter's AuthEnv names is set, the host's credential
// directories are NOT mounted at all.
//
// Mounting them alongside is not merely redundant, it breaks runs. An agent
// CLI that finds stored credentials may prefer them over the environment, so a
// stale or expired login on the host defeats a correctly configured gateway —
// observed as `401 OAuth access token has expired` on a run whose environment
// pointed at a different gateway entirely. The host's settings ride along too:
// a personal session hook mounted into the sandbox tried to execute a binary
// that does not exist inside it.
//
// A run must depend on what it was configured with, not on what happens to be
// lying around in the operator's home directory.
func resolveAuth(a agent.Adapter, logf func(string, ...any)) ([]sandbox.Mount, []string, error) {
	env := authEnv(a.Caps().AuthEnv)
	if len(env) > 0 {
		return nil, env, nil
	}
	mounts, err := authMounts(a.Caps().AuthDirs, logf)
	if err != nil {
		return nil, nil, err
	}
	return mounts, nil, nil
}

// gatewayHosts returns the hosts named by any *_BASE_URL among the forwarded
// auth variables, so the sandbox's egress allowlist follows the gateway the
// operator actually configured.
//
// Without this the allowlist permits Anthropic's own domains and nothing else,
// so pointing an agent at a compatible gateway is refused by the sandbox's
// proxy rather than by anything that explains itself — observed as
// `403 Filtered` inside the session, which reads like an API rejection.
//
// This widens egress only to an endpoint the operator deliberately set as the
// agent's gateway. The allowlist still exists to keep "did the skill reach
// out" answerable, and everything not configured here remains blocked.
func gatewayHosts(env []string) []string {
	var hosts []string
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasSuffix(name, "_BASE_URL") || value == "" {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || u.Host == "" {
			continue
		}
		hosts = append(hosts, u.Hostname())
	}
	return hosts
}

// allowWith returns base plus extra, without duplicates.
func allowWith(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]bool, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, d := range append(append([]string{}, base...), extra...) {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// authMounts expands an adapter's declared auth directories to absolute host
// paths, mounted read-only so subscription auth works inside the sandbox
// without the run being able to modify it.
//
// The host side and the container side are different filesystems with
// different users: a "~/..." entry resolves against the host's own home
// directory for HostPath, but against imagespec.ContainerHome — the home of
// the "runner" user every run executes as — for ContainerPath. Mounting the
// host path verbatim inside the container (the previous behavior) put every
// credential at the host's own path, which the container-side CLI never
// looks in, so no session could ever authenticate.
//
// An auth directory is optional: it holds subscription credentials, not
// something every run requires, and most of them do not exist on a machine
// that has not logged into every agent CLI. A missing one is skipped and
// logged, not an error — failing the run over it, or letting Docker create a
// root-owned placeholder on the host for a bind mount source that does not
// exist, would both be worse than just not mounting it.
func authMounts(dirs []string, logf func(string, ...any)) ([]sandbox.Mount, error) {
	if len(dirs) == 0 {
		return nil, nil
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("runner: resolving home directory for auth mounts: %w", err)
	}
	mounts := make([]sandbox.Mount, 0, len(dirs))
	for _, d := range dirs {
		var hostPath, containerPath string
		switch {
		case strings.HasPrefix(d, "~/"):
			rel := d[2:]
			hostPath = filepath.Join(home, rel)
			containerPath = filepath.Join(imagespec.ContainerHome, rel)
		case !filepath.IsAbs(d):
			hostPath = filepath.Join(home, d)
			containerPath = filepath.Join(imagespec.ContainerHome, d)
		default:
			hostPath, containerPath = d, d
		}

		if _, err := os.Stat(hostPath); err != nil {
			logf("runner: skipping auth mount %s: %v", hostPath, err)
			continue
		}
		mounts = append(mounts, sandbox.Mount{HostPath: hostPath, ContainerPath: containerPath, ReadOnly: true})
	}
	return mounts, nil
}

// authEnv forwards the worker's own environment into the sandbox for each
// name the adapter declares in Caps().AuthEnv that is actually set and
// non-empty. This is the preferred credential path (see Caps.AuthEnv): it
// works on a headless host with no interactive login, unlike authMounts
// above. An unset or empty variable is skipped rather than passed through as
// "NAME=" — a value the CLI would treat as present but wrong, instead of
// absent.
//
// These values are secrets: this function must never log them, only the
// names it forwards (a name reveals nothing; the value does).
// authEnv forwards the worker's own environment into the sandbox for each
// name the adapter declares in Caps().AuthEnv that is actually set and
// non-empty. This is the preferred credential path (see Caps.AuthEnv): it
// works on a headless host with no interactive login, unlike authMounts
// above. An unset or empty variable is skipped rather than passed through as
// "NAME=" — a value the CLI would treat as present but wrong, instead of
// absent.
//
// These values are secrets: this function must never log them, only the
// names it forwards (a name reveals nothing; the value does).
func authEnv(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	env := make([]string, 0, len(names))
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			env = append(env, name+"="+v)
		}
	}
	return env
}

// warnIfNoAuth logs a clear, actionable warning when an adapter declares
// credentials of either kind but neither is actually available: no auth dir
// exists on the host (mounts is empty) and no auth env var is set in the
// worker's own environment. Left unwarned, this surfaces later only as a
// confusing "incomplete panel" with no indication of why. It stays a warning,
// not a hard failure, since the sandbox may be pre-provisioned another way
// (e.g. credentials baked into the image).
func warnIfNoAuth(a agent.Adapter, mounts []sandbox.Mount, logf func(string, ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	caps := a.Caps()
	if len(caps.AuthEnv) == 0 && len(caps.AuthDirs) == 0 {
		return
	}
	if len(mounts) > 0 {
		return
	}
	for _, name := range caps.AuthEnv {
		if os.Getenv(name) != "" {
			return
		}
	}
	logf("runner: %s has no available credentials — no auth dir found on the host and none of %v are set; set one of them or pre-provision the sandbox image", a.Name(), caps.AuthEnv)
}
