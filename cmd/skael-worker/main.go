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

	return workerConfig{
		Config: worker.Config{
			Endpoint:     endpoint,
			APIKey:       apiKey,
			WorkerID:     workerID,
			Lease:        lease,
			PollInterval: poll,
			WorkRoot:     os.Getenv("WORKER_WORK_ROOT"),
		},
		AnthropicAPIKey: anthropicKey,
		Concurrency:     concurrency,
	}, nil
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
		APIKey:     cfg.AnthropicAPIKey,
		HTTPClient: &http.Client{Timeout: 3 * time.Minute},
	})
	if err != nil {
		return fmt.Errorf("skael-worker: LLM gateway: %w", err)
	}

	httpAPI := worker.NewHTTPAPI(cfg.Endpoint, cfg.APIKey)

	r := &realRunner{driver: drv, gateway: gw, concurrency: cfg.Concurrency}

	w, err := worker.New(cfg.Config, httpAPI, r)
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
		Store:         st,
		Driver:        r.driver,
		Gateway:       r.gateway,
		Adapters:      agent.Get,
		Now:           time.Now,
		Sleep:         time.Sleep,
		EngineVersion: version,
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
