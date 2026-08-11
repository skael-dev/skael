package whetstone

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/agentcli"
	"github.com/skael-dev/skael/internal/eval/provider"
	"github.com/skael-dev/skael/internal/ui"
)

// Which backend serves model calls, and with which credentials, is resolved
// by internal/eval/provider — the same package cmd/skael-worker resolves with,
// so one environment configures both and a misconfiguration is described in
// the same words wherever it is met.
//
// timeoutEnv is whetstone's alone: it overrides authoringTimeout without a
// rebuild, and the worker has no interactive call long enough to need it.
const timeoutEnv = "WHETSTONE_LLM_TIMEOUT"

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
	// Models names the model ids LLM_MODEL configured, empty for the shipped
	// defaults.
	Models []string `json:"models,omitempty"`
	// Warnings carries provider.Config.Warnings — configurations that work
	// often enough not to refuse and fail confusingly when they do not.
	Warnings []string `json:"warnings,omitempty"`
	// LLMTimeout is the resolved per-call gateway timeout — authoringTimeout
	// unless WHETSTONE_LLM_TIMEOUT overrides it.
	LLMTimeout string `json:"llm_timeout"`
	// Docker reports whether a docker binary is on PATH. Required by the
	// commands that run a sandbox: eval and suite check.
	Docker bool `json:"docker"`
	// DockerPath is the resolved docker binary, empty when absent.
	DockerPath string `json:"docker_path,omitempty"`
	// Adapters names every registered agent adapter, so a missing blank
	// import shows up here rather than as an unexplained gap in a panel.
	Adapters []string `json:"adapters"`
}

// newGateway builds the backend provider.FromEnv resolved, sharing the store's
// completion cache so a re-run of a generation step costs nothing. The
// return value is wrapped in a progressGateway, so every command that calls
// newGateway prints a line per model call without any change of its own.
func newGateway(cache llm.Cache) (llm.Gateway, error) {
	timeout, err := resolveTimeout()
	if err != nil {
		return nil, err
	}

	gw, err := provider.FromEnv().Gateway(provider.Options{
		Cache:      cache,
		Timeout:    timeout,
		MaxRetries: maxGatewayRetries,
	})
	if err != nil {
		return nil, fmt.Errorf("%w (run `whetstone doctor`)", err)
	}
	return &progressGateway{inner: gw}, nil
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the agent CLI, the LLM gateway, and the sandbox runtime",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		rep, err := RunDoctor(cmd.Context())
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
func RunDoctor(ctx context.Context) (*DoctorReport, error) {
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

	p := provider.FromEnv()
	rep.Gateway = string(p.Kind)
	rep.GatewayDetail = p.Detail
	rep.Models = p.Models
	rep.Warnings = p.Warnings()
	if verr := p.Validate(); verr != nil && p.Kind != provider.KindNone {
		// A named gateway with no credential is not "none" — it is a gateway
		// that cannot authenticate, which is a different fix.
		rep.Warnings = append(rep.Warnings, verr.Error())
	}

	if path, err := exec.LookPath("docker"); err == nil {
		rep.Docker = true
		rep.DockerPath = path
	}

	for _, a := range agent.All() {
		rep.Adapters = append(rep.Adapters, a.Name())
	}

	return rep, nil
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

	if r.Gateway == string(provider.KindNone) {
		ui.Warn("gateway: none — %s", r.GatewayDetail)
	} else {
		ui.Success("gateway: %s — %s", r.Gateway, r.GatewayDetail)
	}
	if len(r.Models) > 0 {
		ui.Info("models: %s (%s)", strings.Join(r.Models, ", "), provider.ModelEnv)
	}
	for _, w := range r.Warnings {
		ui.Warn("%s", w)
	}
	ui.Info("llm timeout: %s (override with %s)", r.LLMTimeout, timeoutEnv)

	if r.Docker {
		ui.Success("docker: %s", r.DockerPath)
	} else {
		ui.Info("docker: not found (required by eval and suite check)")
	}

	ui.Info("agent adapters: %s", strings.Join(r.Adapters, ", "))

}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
