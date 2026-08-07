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
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/cli/whetstone"
	"github.com/skael-dev/skael/internal/eval/agent"

	// Adapters register themselves via init() with agent.Register. A
	// forgotten import here makes agent.Get(name) return (nil, false) with
	// no compile error — the adapter is just silently absent from the panel.
	// checkAdapters below asserts against this at startup.
	_ "github.com/skael-dev/skael/internal/eval/agent/claudecode"
	_ "github.com/skael-dev/skael/internal/eval/agent/codex"
	_ "github.com/skael-dev/skael/internal/eval/agent/cursor"
	_ "github.com/skael-dev/skael/internal/eval/agent/opencode"

	"github.com/skael-dev/skael/internal/eval/derive"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/api"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/docker"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/worker"
)

// wantAdapters is the set of adapters this binary expects to have linked in.
// checkAdapters checks the registry against it so a forgotten blank import
// is logged loudly at startup instead of silently thinning the panel.
var wantAdapters = []string{"claude-code", "codex", "cursor", "opencode"}

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// workerConfig is the resolved, validated configuration for one run of
// skael-worker.
type workerConfig struct {
	worker.Config
	AnthropicAPIKey string
	Concurrency     int
	// LLM gateway overrides for pointing the judge at an Anthropic-compatible
	// gateway (e.g. OpenRouter) instead of the direct Anthropic API. All
	// optional; empty/unset reproduces today's behaviour exactly.
	LLMBaseURL     string
	LLMAuthStyle   api.AuthStyle
	LLMStrongModel string
	LLMFastModel   string
	// RunRoot is where per-session sandbox workspaces are created; empty means
	// os.TempDir(). Only needs setting in a container — see
	// requireHostSharedRoots.
	RunRoot string
	// PanelBaseURL is ANTHROPIC_BASE_URL: the gateway the *panel* dials from
	// inside the sandbox, as opposed to LLMBaseURL, which is the judge's. The
	// worker never dials it, but whether it is set decides the panel's models.
	PanelBaseURL string
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
// environment. ANTHROPIC_API_KEY wires the direct API gateway used for the
// LLM judge, which is always the metered backend. It does not make panel
// execution metered too: the claude-code agent adapter declares AuthDirs
// (~/.claude, ~/.config/claude — see internal/eval/agent/claudecode) which
// internal/eval/runner/session.go mounts into the sandbox, so a panel member
// run through that adapter authenticates with whatever host credentials it
// finds there — subscription-backed wherever those directories exist on the
// host running this worker.
func configFromEnv() (workerConfig, error) {
	endpoint := os.Getenv("SKAEL_ENDPOINT")
	apiKey := os.Getenv("SKAEL_API_KEY")
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")

	var missing []string
	if endpoint == "" {
		missing = append(missing, "SKAEL_ENDPOINT")
	}
	if apiKey == "" {
		missing = append(missing, "SKAEL_API_KEY")
	}
	if anthropicKey == "" {
		missing = append(missing, "ANTHROPIC_API_KEY")
	}
	if len(missing) > 0 {
		return workerConfig{}, fmt.Errorf(
			"skael-worker: missing required environment variable(s): %s "+
				"(ANTHROPIC_API_KEY is the direct API gateway; a subscription CLI on PATH is never used here)",
			joinComma(missing))
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

	authStyle, err := parseLLMAuthStyle(os.Getenv("LLM_AUTH_STYLE"))
	if err != nil {
		return workerConfig{}, err
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
		AnthropicAPIKey: anthropicKey,
		Concurrency:     concurrency,
		LLMBaseURL:      os.Getenv("LLM_BASE_URL"),
		LLMAuthStyle:    authStyle,
		LLMStrongModel:  os.Getenv("LLM_STRONG_MODEL"),
		LLMFastModel:    os.Getenv("LLM_FAST_MODEL"),
		RunRoot:         runRoot,
		PanelBaseURL:    os.Getenv("ANTHROPIC_BASE_URL"),
	}, nil
}

// panelModels resolves the model ids the eval panel should use, empty when the
// shipped default is correct. Gated on PanelBaseURL, not on the model
// variables alone: an operator who set them to pick a cheaper judge must keep
// the panel they had, since a changed panel splits the score trend.
func panelModels(cfg workerConfig) (strong, fast string) {
	if cfg.PanelBaseURL == "" {
		return "", ""
	}
	return cfg.LLMStrongModel, cfg.LLMFastModel
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

// parseLLMAuthStyle resolves LLM_AUTH_STYLE. Empty defaults to
// api.AuthStyleAnthropic (today's x-api-key behaviour); any other value must
// be one of the two the gateway understands.
func parseLLMAuthStyle(v string) (api.AuthStyle, error) {
	switch v {
	case "", string(api.AuthStyleAnthropic):
		return api.AuthStyleAnthropic, nil
	case string(api.AuthStyleBearer):
		return api.AuthStyleBearer, nil
	default:
		return "", fmt.Errorf("skael-worker: LLM_AUTH_STYLE %q is not one of %q, %q", v, api.AuthStyleAnthropic, api.AuthStyleBearer)
	}
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
// Split out from run() so the RunRoot -> StageRoot wiring is testable: nothing
// else exercises it, and getting it wrong voids every task silently.
func deriverOptions(cfg workerConfig, drv sandbox.Driver, gw llm.Gateway) derive.Options {
	return derive.Options{
		Gateway:   gw,
		Driver:    drv,
		StageRoot: cfg.RunRoot,
		Logger:    func(format string, args ...any) { log.Info().Msgf(format, args...) },
	}
}

// run wires the real dependencies (Docker sandbox, API gateway, adapter
// registry) and runs the worker loop until a signal cancels it.
func run(cfg workerConfig) error {
	adapterNames := checkAdapters()
	log.Info().Int("adapters", len(adapterNames)).Strs("names", adapterNames).Msg("skael-worker: agent adapters registered")
	if len(adapterNames) == 0 {
		return errors.New("skael-worker: no agent adapters registered; every blank import is missing")
	}

	drv, err := docker.New(docker.Options{Logger: func(format string, args ...any) {
		log.Info().Msgf(format, args...)
	}})
	if err != nil {
		return fmt.Errorf("skael-worker: docker driver: %w; is Docker installed and running?", err)
	}

	gw, err := api.New(api.Options{
		APIKey:      cfg.AnthropicAPIKey,
		BaseURL:     cfg.LLMBaseURL,
		AuthStyle:   cfg.LLMAuthStyle,
		StrongModel: cfg.LLMStrongModel,
		FastModel:   cfg.LLMFastModel,
		HTTPClient:  &http.Client{Timeout: 3 * time.Minute},
	})
	if err != nil {
		return fmt.Errorf("skael-worker: LLM gateway: %w", err)
	}

	httpAPI := worker.NewHTTPAPI(cfg.Endpoint, cfg.APIKey)

	panelStrong, panelFast := panelModels(cfg)
	if cfg.PanelBaseURL != "" && (panelStrong == "" || panelFast == "") {
		// A warning rather than a refusal: a passthrough proxy in front of
		// Anthropic accepts "opus" happily. The panel health probe is the
		// authority, and it now fails the job with the models named.
		log.Warn().
			Str("anthropic_base_url", cfg.PanelBaseURL).
			Msg("skael-worker: ANTHROPIC_BASE_URL is set but LLM_STRONG_MODEL/LLM_FAST_MODEL are not, " +
				"so the eval panel will ask that gateway for the default Claude Code aliases opus and haiku. " +
				"A gateway that namespaces its model identifiers (OpenRouter uses anthropic/claude-opus-4) " +
				"rejects those and every panel member fails its health probe")
	}

	r := &realRunner{
		driver: drv, gateway: gw, concurrency: cfg.Concurrency, runRoot: cfg.RunRoot,
		panelStrong: panelStrong, panelFast: panelFast, panelBase: cfg.PanelBaseURL,
	}

	der, err := derive.New(deriverOptions(cfg, drv, gw))
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

// checkAdapters confirms every adapter this binary expects (wantAdapters) is
// actually reachable through agent.Get — the guard against a blank import
// that compiles clean but leaves an adapter silently missing from the panel.
func checkAdapters() []string {
	var present []string
	var missing []string
	for _, name := range wantAdapters {
		if _, ok := agent.Get(name); ok {
			present = append(present, name)
		} else {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		log.Error().Strs("missing", missing).Msg("skael-worker: expected agent adapter(s) not registered — check blank imports")
	}
	return present
}

// realRunner implements worker.Runner over whetstone.RunEvalWith, opening
// the workspace store worker.Materialize already built at in.WorkspaceDir
// directly — never walking up the filesystem looking for a .whetstone
// directory the way the interactive `whetstone` CLI does, since that lookup
// depends on the process's current working directory and this worker has no
// reason to have one anywhere near the workspace it was just handed.
type realRunner struct {
	driver      sandbox.Driver
	gateway     llm.Gateway
	concurrency int
	runRoot     string
	panelStrong string
	panelFast   string
	panelBase   string
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

	deps := whetstone.EvalDeps{
		Store:            st,
		Driver:           r.driver,
		Gateway:          r.gateway,
		Adapters:         agent.Get,
		Now:              time.Now,
		Sleep:            time.Sleep,
		EngineVersion:    version,
		WorkspaceRoot:    r.runRoot,
		PanelStrongModel: r.panelStrong,
		PanelFastModel:   r.panelFast,
		PanelBaseURL:     r.panelBase,
	}

	req := evalRequestFrom(in, r.concurrency)

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
	return &worker.DeriveResult{Archive: res.Archive, Checks: res.Checks, Spec: res.Spec}, nil
}

// evalRequestFrom maps a worker.RunInput — what the queue handed the worker
// — onto the whetstone.EvalRequest RunEvalWith actually consumes. This hop
// is the exact seam a prior task's fix round found broken (Panel silently
// dropped on the wire); TestEvalRequestFrom_CarriesTierAndPanel guards it.
func evalRequestFrom(in worker.RunInput, concurrency int) whetstone.EvalRequest {
	if concurrency < 1 {
		concurrency = 1
	}
	return whetstone.EvalRequest{
		Skill:       in.Skill,
		Tier:        runner.Tier(in.Tier),
		Agents:      in.Panel.Agents,
		Models:      in.Panel.Models,
		Concurrency: concurrency,
	}
}
