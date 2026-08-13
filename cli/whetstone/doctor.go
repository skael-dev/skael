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

const timeoutEnv = "WHETSTONE_LLM_TIMEOUT"
const maxGatewayRetries = 3

// authoringTimeout is high because suite drafting can take several minutes.
const authoringTimeout = 10 * time.Minute

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

const versionProbeTimeout = 10 * time.Second

// DoctorReport is the environment health check.
type DoctorReport struct {
	AgentCLI        string   `json:"agent_cli"`
	AgentCLIVersion string   `json:"agent_cli_version,omitempty"`
	AgentCLIError   string   `json:"agent_cli_error,omitempty"`
	Gateway         string   `json:"gateway"`
	GatewayDetail   string   `json:"gateway_detail"`
	Models          []string `json:"models,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
	LLMTimeout      string   `json:"llm_timeout"`
	Docker          bool     `json:"docker"`
	DockerPath      string   `json:"docker_path,omitempty"`
	Adapters        []string `json:"adapters"`
}

// newGateway builds the resolved LLM backend, wrapped in a progress decorator.
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

// RunDoctor collects the environment report. Errors within it (missing CLI,
// missing docker) are folded into the report; only a malformed timeout is
// returned as an error.
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

// probeVersion asks a CLI for its version, returning "" on failure.
func probeVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

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
