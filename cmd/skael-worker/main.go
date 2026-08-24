// Command skael-worker runs the eval queue's claim/materialise/evaluate/report
// loop against real infrastructure: a Docker sandbox, the direct Anthropic
// API gateway, and the linked-in agent adapter registry. It is the
// server-side counterpart to `whetstone eval`, which runs the same engine
// locally against a personal subscription CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/cli/whetstone"
	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/derive"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/provider"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/docker"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/worker"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// judgeTimeout bounds a single judge call. Unlike whetstone's, it is not
// configurable: nothing the worker asks for is an interactive one-shot worth
// waiting ten minutes for.
const judgeTimeout = 3 * time.Minute

// judgeRetries matches whetstone's. Leaving it at zero made the judge try once,
// so a single 429 on any of ~30 grade calls threw away the whole panel run.
const judgeRetries = 3

// judgeGatewayOptions is the worker's gateway configuration, named so a test
// can assert it without building a gateway.
func judgeGatewayOptions() provider.Options {
	return provider.Options{Timeout: judgeTimeout, MaxRetries: judgeRetries}
}

// workerConfig is the resolved, validated configuration for one run of
// skael-worker.
type workerConfig struct {
	worker.Config
	Concurrency      int
	GradeConcurrency int
	// Provider is the resolved LLM backend: the endpoint, the credential, the
	// auth header, and the model ids. It serves the judge in this process and
	// decides the panel's models inside the sandbox — one gateway, resolved
	// once, by the same package `whetstone doctor` reports.
	Provider provider.Config
	// RunRoot is where per-session sandbox workspaces are created; empty means
	// os.TempDir(). Only needs setting in a container — see
	// requireHostSharedRoots.
	RunRoot string
}

func main() {
	platform.InitLogger()

	log.Info().
		Str("version", version).
		Str("commit", commit).
		Str("built", date).
		Msg("skael-worker starting")

	cfg, err := configFromEnv()
	if err != nil {
		log.Error().Err(err).Msg("skael-worker: configuration error")
		os.Exit(1)
	}

	if err := run(cfg); err != nil {
		log.Error().Err(err).Msg("skael-worker: exiting")
		os.Exit(1)
	}
}

// configFromEnv resolves and validates the worker's configuration from the
// environment. The LLM judge is always the metered API backend — hence
// provider.APIFromEnv, which does not consider a subscription CLI on PATH at
// all. That does not make panel execution metered too: the claude-code agent
// adapter declares AuthDirs (~/.claude, ~/.config/claude — see
// internal/eval/agent) which internal/eval/runner/session.go mounts into the
// sandbox, so a panel member run through that adapter authenticates with
// whatever host credentials it finds there — subscription-backed wherever
// those directories exist on the host running this worker.
func configFromEnv() (workerConfig, error) {
	endpoint := os.Getenv("SKAEL_ENDPOINT")
	apiKey := os.Getenv("SKAEL_API_KEY")

	var missing []string
	if endpoint == "" {
		missing = append(missing, "SKAEL_ENDPOINT")
	}
	if apiKey == "" {
		missing = append(missing, "SKAEL_API_KEY")
	}
	if len(missing) > 0 {
		return workerConfig{}, fmt.Errorf(
			"skael-worker: missing required environment variable(s): %s", joinComma(missing))
	}

	prov := provider.APIFromEnv()
	if err := prov.Validate(); err != nil {
		return workerConfig{}, fmt.Errorf(
			"skael-worker: no LLM gateway for the judge: %w. A subscription CLI on PATH is never used here, "+
				"because a published score must come from a metered, reproducible backend", err)
	}

	workerID := os.Getenv("WORKER_ID")
	if workerID == "" {
		host, err := os.Hostname()
		if err != nil {
			host = "unknown-host"
		}
		workerID = fmt.Sprintf("%s-%d", host, os.Getpid())
	}

	lease, err := parseDurationEnv("WORKER_LEASE", 5*time.Minute)
	if err != nil {
		return workerConfig{}, err
	}
	poll, err := parseDurationEnv("WORKER_POLL", 15*time.Second)
	if err != nil {
		return workerConfig{}, err
	}

	concurrency := 1
	if v := os.Getenv("WORKER_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return workerConfig{}, fmt.Errorf("skael-worker: WORKER_CONCURRENCY %q is not a positive integer", v)
		}
		concurrency = n
	}

	// A separate knob from WORKER_CONCURRENCY: a sandbox container is bounded
	// by CPU and memory, a judge call by the account's rate limit. Zero here
	// falls back to whetstone's own default, not to the session count.
	gradeConcurrency := 0
	if v := os.Getenv("WORKER_GRADE_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return workerConfig{}, fmt.Errorf("skael-worker: WORKER_GRADE_CONCURRENCY %q is not a positive integer", v)
		}
		gradeConcurrency = n
	}

	runRoot := os.Getenv("WORKER_RUN_ROOT")
	workRoot := os.Getenv("WORKER_WORK_ROOT")
	if err := requireHostSharedRoots(runRoot, workRoot, inContainer()); err != nil {
		return workerConfig{}, err
	}

	return workerConfig{
		Config: worker.Config{
			Endpoint:     endpoint,
			APIKey:       apiKey,
			WorkerID:     workerID,
			Lease:        lease,
			PollInterval: poll,
			WorkRoot:     workRoot,
		},
		Concurrency:      concurrency,
		GradeConcurrency: gradeConcurrency,
		Provider:         prov,
		RunRoot:          runRoot,
	}, nil
}

// requireHostSharedRoots refuses to start a containerized worker that has not
// been told where the directories it hands to the Docker daemon live.
//
// The daemon resolves every bind source in the host's filesystem, and a
// container's /tmp names nothing there. Docker's response to a missing bind
// source is to create an empty directory, not to fail — so an unset root
// yields a sandbox with no task and no skill (WORKER_RUN_ROOT) or a verifier
// script that isn't there (WORKER_WORK_ROOT), neither distinguishable
// downstream from a genuinely bad skill.
func requireHostSharedRoots(runRoot, workRoot string, containerized bool) error {
	if !containerized {
		return nil
	}
	var missing []string
	if runRoot == "" {
		missing = append(missing, "WORKER_RUN_ROOT")
	}
	if workRoot == "" {
		missing = append(missing, "WORKER_WORK_ROOT")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"skael-worker: running inside a container without %s. Sandbox workspaces and the suite verifier "+
			"are bind-mounted into containers started through the Docker socket, and the daemon resolves "+
			"those paths on the host — a path under this container's own /tmp does not exist there, and "+
			"Docker would silently mount an empty directory, scoring every skill as though it did nothing. "+
			"Set each to a directory bind-mounted at the SAME path on both sides "+
			"(e.g. `-v /var/lib/skael/run:/var/lib/skael/run` with WORKER_RUN_ROOT=/var/lib/skael/run)",
		joinComma(missing))
}

// inContainer reports whether this process looks containerized. Docker writes
// /.dockerenv into every container it creates; /run/.containerenv is Podman's
// equivalent. Both are heuristics, which is why they only ever gate an error
// that an explicit WORKER_RUN_ROOT already satisfies — a false positive is
// cleared by setting the variable, and a false negative just restores the
// previous behaviour.
func inContainer() bool {
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func parseDurationEnv(name string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("skael-worker: %s %q is not a valid duration: %w", name, v, err)
	}
	return d, nil
}

func joinComma(ss []string) string {
	out := ss[0]
	for _, s := range ss[1:] {
		out += ", " + s
	}
	return out
}

// deriverOptions builds the deriver's configuration from the worker's own.
func deriverOptions(gw llm.Gateway) derive.Options {
	return derive.Options{
		Gateway: gw,
		Logger:  func(format string, args ...any) { log.Info().Msgf(format, args...) },
	}
}

// run wires the real dependencies (Docker sandbox, API gateway, adapter
// registry) and runs the worker loop until a signal cancels it.
func run(cfg workerConfig) error {

	drv, err := docker.New(docker.Options{Logger: func(format string, args ...any) {
		log.Info().Msgf(format, args...)
	}})
	if err != nil {
		return fmt.Errorf("skael-worker: docker driver: %w; is Docker installed and running?", err)
	}

	gw, err := cfg.Provider.Gateway(judgeGatewayOptions())
	if err != nil {
		return fmt.Errorf("skael-worker: LLM gateway: %w", err)
	}

	httpAPI := worker.NewHTTPAPI(cfg.Endpoint, cfg.APIKey)

	// The same warnings `whetstone doctor` prints, from the same place.
	for _, w := range cfg.Provider.Warnings() {
		log.Warn().Msgf("skael-worker: %s", w)
	}

	r := &realRunner{
		driver: drv, gateway: gw, concurrency: cfg.Concurrency,
		gradeConcurrency: cfg.GradeConcurrency, runRoot: cfg.RunRoot,
		panelModels: cfg.Provider.PanelModels(), panelBase: cfg.Provider.BaseURL,
		panelExcludeEnv: cfg.Provider.PanelExcludeEnv(),
	}

	der, err := derive.New(deriverOptions(gw))
	if err != nil {
		return fmt.Errorf("skael-worker: suite deriver: %w", err)
	}

	w, err := worker.New(cfg.Config, httpAPI, r, &realDeriver{d: der})
	if err != nil {
		return fmt.Errorf("skael-worker: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Info().
		Str("endpoint", cfg.Endpoint).
		Str("worker_id", cfg.WorkerID).
		Dur("lease", cfg.Lease).
		Dur("poll_interval", cfg.PollInterval).
		Msg("skael-worker: polling for jobs")

	err = pollLoop(ctx, w, cfg.PollInterval)
	if errors.Is(err, context.Canceled) {
		log.Info().Msg("skael-worker: shutting down on signal")
		return nil
	}
	return err
}

// pollLoop drives worker.Worker.RunOnce itself, rather than calling
// worker.Loop directly, so it can log an explicit "queue empty" line on
// every idle poll — an operator watching this binary's logs otherwise has no
// way to tell a genuinely idle worker from one that has silently wedged.
func pollLoop(ctx context.Context, w *worker.Worker, pollInterval time.Duration) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		worked, err := w.RunOnce(ctx)
		if err != nil {
			log.Error().Err(err).Msg("skael-worker: run failed")
		}
		if worked {
			continue
		}
		log.Info().Msg("skael-worker: queue empty")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// realRunner implements worker.Runner over whetstone.RunEvalWith, opening
// the workspace store worker.Materialize already built at in.WorkspaceDir
// directly — never walking up the filesystem looking for a .whetstone
// directory the way the interactive `whetstone` CLI does, since that lookup
// depends on the process's current working directory and this worker has no
// reason to have one anywhere near the workspace it was just handed.
type realRunner struct {
	driver           sandbox.Driver
	gateway          llm.Gateway
	concurrency      int
	gradeConcurrency int
	runRoot          string
	panelModels      []string
	panelBase        string
	panelExcludeEnv  []string
}

// evalDepsFrom maps the resolved worker onto the deps RunEvalWith consumes.
// It is a separate function for the same reason evalRequestFrom is: this hop
// carries panel configuration that nothing downstream can reconstruct, and a
// field dropped here is not an error but a run against a panel nobody chose.
func evalDepsFrom(r *realRunner, st *store.Store) whetstone.EvalDeps {
	return whetstone.EvalDeps{
		Store:           st,
		Driver:          r.driver,
		Gateway:         r.gateway,
		Adapters:        agent.Get,
		Now:             time.Now,
		Sleep:           time.Sleep,
		EngineVersion:   version,
		WorkspaceRoot:   r.runRoot,
		PanelModels:     r.panelModels,
		PanelBaseURL:    r.panelBase,
		PanelExcludeEnv: r.panelExcludeEnv,
	}
}

func (r *realRunner) Run(ctx context.Context, in worker.RunInput) (*report.Report, error) {
	st, err := store.Open(in.WorkspaceDir)
	if err != nil {
		return nil, fmt.Errorf("skael-worker: open workspace store at %s: %w", in.WorkspaceDir, err)
	}
	defer func() { _ = st.Close() }()

	log.Info().
		Str("job_id", string(in.JobID)).
		Str("skill", in.Skill).
		Int("version", in.Version).
		Str("suite_ref", in.SuiteRef).
		Str("tier", in.Tier).
		Msg("skael-worker: claimed job")

	deps := evalDepsFrom(r, st)

	req := evalRequestFrom(in, r.concurrency, r.gradeConcurrency)

	rep, err := whetstone.RunEvalWith(ctx, deps, req)
	if err != nil {
		log.Error().
			Str("job_id", string(in.JobID)).
			Str("skill", in.Skill).
			Str("suite_ref", in.SuiteRef).
			Err(err).
			Msg("skael-worker: job failed")
		return nil, err
	}

	log.Info().
		Str("job_id", string(in.JobID)).
		Str("skill", in.Skill).
		Int("version", in.Version).
		Str("suite_ref", in.SuiteRef).
		Str("tier", in.Tier).
		Float64("headline", rep.Headline).
		Msg("skael-worker: job completed")

	return rep, nil
}

// realDeriver adapts derive.Deriver to worker.Deriver. The two Input types are
// separate because internal/worker must not import internal/eval/derive —
// that is what keeps the run loop testable with no LLM and no Docker.
type realDeriver struct{ d *derive.Deriver }

func (r *realDeriver) Derive(ctx context.Context, in worker.DeriveInput) (*worker.DeriveResult, error) {
	panel, err := runner.ParsePanel(in.Panel.Agents, in.Panel.Models)
	if err != nil {
		return nil, fmt.Errorf("skael-worker: derive: %w", err)
	}
	res, err := r.d.Derive(ctx, derive.Input{
		Skill: in.Skill, Bundle: in.Bundle, Tier: in.Tier, Panel: panel,
	})
	if err != nil {
		return nil, err
	}
	return &worker.DeriveResult{Archive: res.Archive, Tasks: res.Tasks, Spec: res.Spec}, nil
}

// evalRequestFrom maps a worker.RunInput — what the queue handed the worker
// — onto the whetstone.EvalRequest RunEvalWith actually consumes. This hop
// is the exact seam a prior task's fix round found broken (Panel silently
// dropped on the wire); TestEvalRequestFrom_CarriesTierAndPanel guards it.
func evalRequestFrom(in worker.RunInput, concurrency, gradeConcurrency int) whetstone.EvalRequest {
	if concurrency < 1 {
		concurrency = 1
	}
	return whetstone.EvalRequest{
		Skill:            in.Skill,
		Tier:             runner.Tier(in.Tier),
		Agents:           in.Panel.Agents,
		Models:           in.Panel.Models,
		AllowVoid:        in.AllowVoid,
		Concurrency:      concurrency,
		GradeConcurrency: gradeConcurrency,
	}
}
