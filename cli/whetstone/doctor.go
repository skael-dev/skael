package whetstone

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/agentcli"
	"github.com/skael-dev/skael/internal/eval/llm/api"
	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/ui"
)

// Gateway kinds reported by doctor and selected by every model-calling
// command.
const (
	gatewaySubscription = "subscription"
	gatewayAPI          = "api"
	gatewayNone         = "none"
)

// Neither llm gateway package reads the environment — both take their
// credentials as options — so mapping the environment onto them is the CLI's
// job. These are the Anthropic SDK's own variable names rather than
// whetstone-specific ones, so a machine already set up for the API needs no
// further configuration.
const (
	apiKeyEnv     = "ANTHROPIC_API_KEY"
	apiBaseURLEnv = "ANTHROPIC_BASE_URL"
	// apiAuthTokenEnv is the Claude Code CLI's own name for a bearer token,
	// used together with apiBaseURLEnv to point at an Anthropic-compatible
	// gateway such as OpenRouter. Preferred over apiKeyEnv when both are set,
	// since a bearer token implies a non-Anthropic gateway is intended.
	apiAuthTokenEnv = "ANTHROPIC_AUTH_TOKEN"
	// Model overrides, named to match the worker's so one environment
	// configures both. Without these, pointing apiBaseURLEnv at a non-Anthropic
	// gateway still asks it for Anthropic's own model names — OpenRouter
	// namespaces its identifiers (anthropic/claude-opus-4), so the request
	// 404s and authoring fails with a confusing "no endpoints found".
	strongModelEnv = "LLM_STRONG_MODEL"
	fastModelEnv   = "LLM_FAST_MODEL"
	// timeoutEnv overrides authoringTimeout without a rebuild.
	timeoutEnv = "WHETSTONE_LLM_TIMEOUT"
)

// maxGatewayRetries bounds retries of transient upstream failures. Generation
// is a several-call sequence, and losing all of it to one 529 is not
// acceptable.
const maxGatewayRetries = 3

// authoringTimeout is the default bound on a single gateway call, overridable
// with WHETSTONE_LLM_TIMEOUT. Both gateway packages default to three minutes,
// which is too short for this CLI's longest call: suite drafting asks for ten
// task packages, each with a prompt, an oracle script, and a verifier script,
// in one response. Running `whetstone new` against a real CLI killed that
// call at exactly the three-minute mark, twice, after every earlier pass had
// succeeded. Authoring is interactive and one-shot, so waiting longer costs
// far less than discarding the run. The resources pass no longer needs a
// large timeout itself — it asks for one file per call rather than every
// planned file in one response — but suite drafting still can, so the
// default stays high with an escape hatch for whatever the next long call
// turns out to be.
const authoringTimeout = 10 * time.Minute

// resolveTimeout reads timeoutEnv, defaulting to authoringTimeout. A
// malformed value fails loudly and names the offending value, matching the
// worker's own env-duration convention (parseDurationEnv in
// cmd/skael-worker/main.go) rather than silently falling back — the CLI is
// interactive, so a typo is better caught here than discovered as an
// unexpectedly short (or long) run.
func resolveTimeout() (time.Duration, error) {
	v := os.Getenv(timeoutEnv)
	if v == "" {
		return authoringTimeout, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("whetstone: %s %q is not a valid duration: %w", timeoutEnv, v, err)
	}
	return d, nil
}

// versionProbeTimeout bounds the `<cli> --version` call doctor makes. A
// diagnostic that hangs is worse than one that reports nothing.
const versionProbeTimeout = 10 * time.Second

// DoctorReport is what `whetstone doctor` found. Every field is filled on
// every run, including the failures: doctor is run when something is already
// wrong, so it diagnoses rather than errors.
type DoctorReport struct {
	// AgentCLI is the resolved path of the agent CLI on PATH, empty when none
	// was found.
	AgentCLI string `json:"agent_cli"`
	// AgentCLIVersion is what that CLI reported for --version.
	AgentCLIVersion string `json:"agent_cli_version,omitempty"`
	// AgentCLIError explains why no CLI is usable, when none is.
	AgentCLIError string `json:"agent_cli_error,omitempty"`
	// Gateway is which backend would serve model calls: subscription, api, or
	// none.
	Gateway string `json:"gateway"`
	// GatewayDetail explains that choice, including why it is "none".
	GatewayDetail string `json:"gateway_detail"`
	// LLMTimeout is the resolved per-call gateway timeout — authoringTimeout
	// unless WHETSTONE_LLM_TIMEOUT overrides it.
	LLMTimeout string `json:"llm_timeout"`
	// Docker reports whether a docker binary is on PATH. Required by the
	// commands that run a sandbox: eval, repair, and suite check.
	Docker bool `json:"docker"`
	// DockerPath is the resolved docker binary, empty when absent.
	DockerPath string `json:"docker_path,omitempty"`
	// Adapters names every registered agent adapter, so a missing blank
	// import shows up here rather than as an unexplained gap in a panel.
	Adapters []string `json:"adapters"`
	// Judge is the calibration result, present only when --judge was passed.
	Judge *score.CalResult `json:"judge,omitempty"`
	// JudgeError explains why calibration was not run, when --judge was
	// passed but no gateway is available. doctor diagnoses; it does not fail.
	JudgeError string `json:"judge_error,omitempty"`
}

// gatewayChoice is the decision doctor reports and newGateway acts on.
type gatewayChoice struct {
	Kind   string
	Detail string
	Binary string
}

// chooseGateway prefers the subscription gateway: calls made through an agent
// CLI are billed to a subscription the author already has, where the direct
// API needs a key and bills separately.
func chooseGateway() gatewayChoice {
	// Explicit gateway configuration beats autodetection. Setting a base URL
	// or a bearer token is an unambiguous statement that a particular gateway
	// is intended; silently preferring a subscription CLI that happens to be
	// on PATH would bill the wrong account and quietly evaluate against a
	// different model than the one configured.
	//
	// ANTHROPIC_API_KEY alone stays *below* the CLI: it is present on plenty
	// of developer machines that also have the CLI installed, and treating it
	// as an override would change today's behaviour for them.
	if os.Getenv(apiBaseURLEnv) != "" || os.Getenv(apiAuthTokenEnv) != "" {
		return gatewayChoice{
			Kind:   gatewayAPI,
			Detail: fmt.Sprintf("direct API, configured explicitly via %s/%s", apiBaseURLEnv, apiAuthTokenEnv),
		}
	}
	if bin, err := agentcli.Detect(); err == nil {
		return gatewayChoice{
			Kind:   gatewaySubscription,
			Binary: bin,
			Detail: fmt.Sprintf("agent CLI %s, billed to your subscription", bin),
		}
	}
	if os.Getenv(apiAuthTokenEnv) != "" {
		return gatewayChoice{
			Kind:   gatewayAPI,
			Detail: fmt.Sprintf("direct API, authenticated with %s", apiAuthTokenEnv),
		}
	}
	if os.Getenv(apiKeyEnv) != "" {
		return gatewayChoice{
			Kind:   gatewayAPI,
			Detail: fmt.Sprintf("direct API, authenticated with %s", apiKeyEnv),
		}
	}
	return gatewayChoice{
		Kind:   gatewayNone,
		Detail: fmt.Sprintf("no supported agent CLI on PATH and neither %s nor %s is set", apiKeyEnv, apiAuthTokenEnv),
	}
}

// newGateway builds the gateway chooseGateway selected, sharing the store's
// completion cache so a re-run of a generation step costs nothing. The
// return value is wrapped in a progressGateway, so every command that calls
// newGateway prints a line per model call without any change of its own.
func newGateway(cache llm.Cache) (llm.Gateway, error) {
	timeout, err := resolveTimeout()
	if err != nil {
		return nil, err
	}

	var gw llm.Gateway
	switch c := chooseGateway(); c.Kind {
	case gatewaySubscription:
		gw, err = agentcli.New(agentcli.Options{
			Binary:     c.Binary,
			Cache:      cache,
			Timeout:    timeout,
			MaxRetries: maxGatewayRetries,
		})
	case gatewayAPI:
		authStyle := api.AuthStyleAnthropic
		key := os.Getenv(apiKeyEnv)
		if token := os.Getenv(apiAuthTokenEnv); token != "" {
			authStyle = api.AuthStyleBearer
			key = token
		}
		gw, err = api.New(api.Options{
			BaseURL:     os.Getenv(apiBaseURLEnv),
			APIKey:      key,
			AuthStyle:   authStyle,
			StrongModel: os.Getenv(strongModelEnv),
			FastModel:   os.Getenv(fastModelEnv),
			Cache:       cache,
			HTTPClient:  &http.Client{Timeout: timeout},
			MaxRetries:  maxGatewayRetries,
		})
	default:
		return nil, fmt.Errorf("no LLM gateway available: %s (run `whetstone doctor`)", c.Detail)
	}
	if err != nil {
		return nil, err
	}
	return &progressGateway{inner: gw}, nil
}

var doctorJudge bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the agent CLI, the LLM gateway, and the sandbox runtime",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		rep, err := RunDoctor(cmd.Context(), doctorJudge)
		if err != nil {
			return err
		}
		if ui.JSONMode {
			return ui.PrintJSON(rep)
		}
		rep.render()
		return nil
	},
}

// RunDoctor collects the environment report. It returns an error only when
// the report itself could not be produced — a missing CLI, a missing docker,
// and an unusable gateway are all reported in the report, because the command
// exists to be run when something is already broken. A malformed
// WHETSTONE_LLM_TIMEOUT is the one exception: unlike a missing CLI, there is
// no resolved value left to report, so it is returned as an error rather than
// folded into the report.
func RunDoctor(ctx context.Context, withJudge bool) (*DoctorReport, error) {
	rep := &DoctorReport{}

	timeout, err := resolveTimeout()
	if err != nil {
		return nil, err
	}
	rep.LLMTimeout = timeout.String()

	if bin, err := agentcli.Detect(); err == nil {
		rep.AgentCLI = bin
		rep.AgentCLIVersion = probeVersion(bin)
	} else {
		rep.AgentCLIError = err.Error()
	}

	c := chooseGateway()
	rep.Gateway = c.Kind
	rep.GatewayDetail = c.Detail

	if path, err := exec.LookPath("docker"); err == nil {
		rep.Docker = true
		rep.DockerPath = path
	}

	for _, a := range agent.All() {
		rep.Adapters = append(rep.Adapters, a.Name())
	}

	if withJudge {
		runJudgeCalibration(ctx, rep)
	}

	return rep, nil
}

// runJudgeCalibration fills in rep.Judge or rep.JudgeError. doctor diagnoses
// rather than fails, so no gateway means a reported reason, not an error
// returned from RunDoctor.
func runJudgeCalibration(ctx context.Context, rep *DoctorReport) {
	gw, err := newGateway(nil)
	if err != nil {
		rep.JudgeError = fmt.Sprintf("not run — no gateway (%s)", err)
		return
	}

	set, err := score.Calibration()
	if err != nil {
		rep.JudgeError = fmt.Sprintf("not run — %s", err)
		return
	}

	j, err := score.NewJudge(score.JudgeOptions{
		Gateway: gw,
		Spec:    &spec.SkillSpec{Name: "calibration", Purpose: "judge calibration"},
	})
	if err != nil {
		rep.JudgeError = fmt.Sprintf("not run — %s", err)
		return
	}

	result, err := score.Calibrate(ctx, j, set)
	if err != nil {
		rep.JudgeError = fmt.Sprintf("not run — %s", err)
		return
	}
	rep.Judge = &result
}

// probeVersion asks a CLI for its version, returning "" rather than failing:
// an unrecognised --version flag is worth reporting as unknown, not worth
// aborting the diagnosis over.
func probeVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// render prints the report as a human-readable checklist.
func (r *DoctorReport) render() {
	if r.AgentCLI != "" {
		version := r.AgentCLIVersion
		if version == "" {
			version = "version unknown"
		}
		ui.Success("agent CLI: %s (%s)", r.AgentCLI, version)
	} else {
		ui.Warn("agent CLI: not found (%s)", r.AgentCLIError)
	}

	if r.Gateway == gatewayNone {
		ui.Warn("gateway: none — %s", r.GatewayDetail)
	} else {
		ui.Success("gateway: %s — %s", r.Gateway, r.GatewayDetail)
	}
	ui.Info("llm timeout: %s (override with %s)", r.LLMTimeout, timeoutEnv)

	if r.Docker {
		ui.Success("docker: %s", r.DockerPath)
	} else {
		ui.Info("docker: not found (required by eval, repair, and suite check)")
	}

	ui.Info("agent adapters: %s", strings.Join(r.Adapters, ", "))

	if r.Judge != nil {
		j := r.Judge
		if j.JudgeTrusted() {
			ui.Success("judge: κ=%.2f over %d %s-labelled items — trusted", j.Kappa, j.N, j.LabeledBy)
		} else {
			ui.Warn("judge: κ=%.2f over %d %s-labelled items — advisory only, Uplift falls back to the pass-rate delta", j.Kappa, j.N, j.LabeledBy)
		}
	} else if r.JudgeError != "" {
		ui.Warn("judge: %s", r.JudgeError)
	}
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJudge, "judge", false, "run judge calibration against the labelled set and report Cohen's κ")
	rootCmd.AddCommand(doctorCmd)
}
