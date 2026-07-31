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
)

// maxGatewayRetries bounds retries of transient upstream failures. Generation
// is a several-call sequence, and losing all of it to one 529 is not
// acceptable.
const maxGatewayRetries = 3

// authoringTimeout bounds a single gateway call. Both gateway packages
// default to three minutes, which is too short for this CLI's longest call:
// suite drafting asks for ten task packages, each with a prompt, an oracle
// script, and a verifier script, in one response. Running `whetstone new`
// against a real CLI killed that call at exactly the three-minute mark, twice,
// after every earlier pass had succeeded. Authoring is interactive and
// one-shot, so waiting longer costs far less than discarding the run.
const authoringTimeout = 10 * time.Minute

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
	// Docker reports whether a docker binary is on PATH. Informational for
	// now: nothing in this phase runs a sandbox.
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
	if bin, err := agentcli.Detect(); err == nil {
		return gatewayChoice{
			Kind:   gatewaySubscription,
			Binary: bin,
			Detail: fmt.Sprintf("agent CLI %s, billed to your subscription", bin),
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
		Detail: fmt.Sprintf("no supported agent CLI on PATH and %s is unset", apiKeyEnv),
	}
}

// newGateway builds the gateway chooseGateway selected, sharing the store's
// completion cache so a re-run of a generation step costs nothing.
func newGateway(cache llm.Cache) (llm.Gateway, error) {
	switch c := chooseGateway(); c.Kind {
	case gatewaySubscription:
		return agentcli.New(agentcli.Options{
			Binary:     c.Binary,
			Cache:      cache,
			Timeout:    authoringTimeout,
			MaxRetries: maxGatewayRetries,
		})
	case gatewayAPI:
		return api.New(api.Options{
			BaseURL:    os.Getenv(apiBaseURLEnv),
			APIKey:     os.Getenv(apiKeyEnv),
			Cache:      cache,
			HTTPClient: &http.Client{Timeout: authoringTimeout},
			MaxRetries: maxGatewayRetries,
		})
	default:
		return nil, fmt.Errorf("no LLM gateway available: %s (run `whetstone doctor`)", c.Detail)
	}
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
// exists to be run when something is already broken.
func RunDoctor(ctx context.Context, withJudge bool) (*DoctorReport, error) {
	rep := &DoctorReport{}

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

	if r.Docker {
		ui.Success("docker: %s", r.DockerPath)
	} else {
		ui.Info("docker: not found (only needed once sandboxed evaluation lands)")
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
