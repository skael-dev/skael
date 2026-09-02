package whetstone

import (
	"context"
	"errors"
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
	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/resolve"
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

var checkEgressFlag bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the agent CLI, the LLM gateway, and the sandbox runtime",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		rep, err := RunDoctor(cmd.Context())
		if err != nil {
			return err
		}
		cfg := resolve.FromEnv(nil)
		if ui.JSONMode {
			if err := ui.PrintJSON(rep); err != nil {
				return err
			}
		} else {
			rep.render()
			fmt.Print(doctorReport(cfg))
		}
		if checkEgressFlag {
			if err := checkEgress(cmd.Context(), cfg); err != nil {
				return err
			}
			ui.Success("egress check: the sandbox could not reach the network under a deny-all policy")
		}
		return nil
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&checkEgressFlag, "check-egress", false,
		"run a denied network request against the real Kubernetes cluster to confirm SANDBOX_K8S_NETWORK_POLICY")
}

// doctorReport describes the resolved sandbox driver. It exists so a setup
// problem is found here rather than in a score.
func doctorReport(cfg resolve.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "sandbox driver: %s\n", cfg.Driver)
	if cfg.Driver == "kubernetes" {
		fmt.Fprintf(&b, "  namespace: %s\n", cfg.K8s.Namespace)
		fmt.Fprintf(&b, "  image:     %s\n", cfg.K8s.Image)
		if cfg.K8s.RuntimeClass != "" {
			fmt.Fprintf(&b, "  runtime class: %s\n", cfg.K8s.RuntimeClass)
		}
	}
	for _, w := range cfg.Warnings() {
		fmt.Fprintf(&b, "  warning: %s\n", w)
	}
	return b.String()
}

// checkEgress asks the cluster whether it really enforces the policy the
// operator asserted. A denied request is the pass; a request that reaches the
// network means the CNI ignores NetworkPolicy, which is the one guarantee
// this driver cannot verify on its own.
func checkEgress(ctx context.Context, cfg resolve.Config) error {
	drv, err := cfg.Build(nil)
	if err != nil {
		return err
	}
	img, err := drv.Prepare(ctx, sandbox.EnvSpec{Skill: "doctor"})
	if err != nil {
		return err
	}
	workDir, err := os.MkdirTemp("", "skael-doctor-egress-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)
	res, err := drv.Run(ctx, sandbox.RunSpec{
		Image:     img,
		Workspace: workDir,
		Argv:      []string{"sh", "-c", "curl -sS -m 5 https://example.com >/dev/null"},
		Network:   sandbox.NetNone,
		Timeout:   2 * time.Minute,
	})
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		return errors.New("the sandbox reached the network under a deny-all policy: this cluster's CNI does not enforce NetworkPolicy, so SANDBOX_K8S_NETWORK_POLICY=true is wrong here and every restricted session is unrestricted")
	}
	return nil
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
