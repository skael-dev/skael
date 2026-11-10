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
	"github.com/skael-dev/skael/internal/eval/store"
)

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
				// Lost events degrade a resume to Unknown, so do not record
				// this as OK or Failed — those statuses skip retries.
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

	ws, err = stageEvalWorkspace(in.SuiteDir, ev, r.o.WorkspaceRoot)
	if err != nil {
		return finish(store.StatusError, err)
	}
	defer func() {
		if rmErr := os.RemoveAll(ws); rmErr != nil {
			r.o.Logger("runner: removing workspace %s: %v", ws, rmErr)
		}
	}()

	if k.Condition == CondSkill {
		if err := a.InstallSkill(ws, in.BundleDir); err != nil {
			return finish(store.StatusError, fmt.Errorf("runner: installing skill: %w", err))
		}
		skipDirs = []string{a.Caps().SkillDir}
	}

	authVars, err := r.authFor(k.Agent, k.Model)
	if err != nil {
		return finish(store.StatusError, err)
	}
	exec := sandbox.NewExec(r.o.Driver, sandbox.RunSpec{
		Image:     in.Image,
		Workspace: ws,
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

	return finish(store.StatusOK, nil)
}

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
			skipDirs := []string{a.Caps().SkillDir}
			if _, wErr := WriteArtifacts(artifactDir, raw, po.Events, g, ws, skipDirs); wErr != nil {
				r.o.Logger("runner: writing artifacts for probe %+v: %v", k, wErr)
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

	authVars, err := r.authFor(p.Member.Agent, p.Member.Model)
	if err != nil {
		return finish(err)
	}
	exec := sandbox.NewExec(r.o.Driver, sandbox.RunSpec{
		Image:     in.Image,
		Workspace: ws,
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
func loadProbeEvents(artifactDir string) ([]agent.Event, error) {
	if artifactDir == "" {
		return nil, errors.New("no artifact directory recorded")
	}
	f, err := os.Open(filepath.Join(artifactDir, "events.jsonl"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var events []agent.Event
	dec := json.NewDecoder(f)
	for dec.More() {
		var e agent.Event
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("decoding event: %w", err)
		}
		events = append(events, e)
	}
	return events, nil
}

// invoke runs one agent session. A session the harness reports as an error
// (Meta.IsError) could not be performed at all: it is a hard failure that
// carries the provider's own message, so ClaimRun retries it on the next
// Execute instead of recording it as evidence about the skill. The returned
// status is empty on success, and the raw bytes are the native stream.
func (r *Runner) invoke(ctx context.Context, a agent.Adapter, spec agent.InvokeSpec, k store.RunKey) (*agent.Result, []byte, string, error) {
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
	if result.Meta.IsError {
		msg := result.Meta.Error
		if msg == "" {
			msg = "the harness reported an error"
		}
		return result, raw, store.StatusError, fmt.Errorf("runner: %s on %s: %s", k.TaskID, k.Model, msg)
	}
	return result, raw, "", nil
}

// authFor is one member's session environment. The sandbox inherits nothing
// from this process, so a member MemberAuth does not know cannot run.
func (r *Runner) authFor(agentName, model string) ([]string, error) {
	if r.o.MemberAuth != nil {
		if env, ok := r.o.MemberAuth(agentName, model); ok {
			return env, nil
		}
	}
	return nil, fmt.Errorf("runner: no provider is configured for %s on %s", model, agentName)
}

// gatewayHosts extracts hostnames from *_BASE_URL variables so the sandbox's
// egress allowlist permits the operator's configured gateway.
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
