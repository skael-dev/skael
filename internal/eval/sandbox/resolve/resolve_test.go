package resolve_test

import (
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
	"github.com/skael-dev/skael/internal/eval/sandbox/resolve"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestFromEnv_DefaultsToDocker(t *testing.T) {
	c := resolve.FromEnv(env(nil))
	if c.Driver != "docker" {
		t.Errorf("Driver = %q, want docker: an unset SANDBOX_DRIVER must behave exactly as before", c.Driver)
	}
	if !resolve.RequiresHostSharedRoots(c) {
		t.Error("the docker driver bind-mounts workspaces, so WORKER_RUN_ROOT still applies")
	}
}

func TestFromEnv_ReadsTheKubernetesOptions(t *testing.T) {
	c := resolve.FromEnv(env(map[string]string{
		"SANDBOX_DRIVER":                "kubernetes",
		"SANDBOX_K8S_NAMESPACE":         "skael-sandbox",
		"SANDBOX_K8S_IMAGE":             "ghcr.io/skael-dev/whetstone-base:1",
		"SANDBOX_K8S_RUNTIME_CLASS":     "kata",
		"SANDBOX_K8S_HARDWARE_ISOLATED": "true",
		"SANDBOX_K8S_NETWORK_POLICY":    "true",
	}))
	if c.Driver != "kubernetes" || c.K8s.Namespace != "skael-sandbox" || c.K8s.Image == "" {
		t.Fatalf("config not read: %+v", c)
	}
	if !c.K8s.HardwareIsolated || !c.K8s.NetworkPolicyEnforced || c.K8s.RuntimeClass != "kata" {
		t.Errorf("assertions not read: %+v", c.K8s)
	}
	// No bind mounts, so the constraint that makes a containerized Docker
	// worker awkward does not apply.
	if resolve.RequiresHostSharedRoots(c) {
		t.Error("the kubernetes driver bind-mounts nothing; WORKER_RUN_ROOT must not be required")
	}
}

func TestFromEnv_ReadsTheNorthflankOptions(t *testing.T) {
	c := resolve.FromEnv(env(map[string]string{
		"SANDBOX_DRIVER":             "northflank",
		"SANDBOX_NF_TOKEN":           "nf_secret",
		"SANDBOX_NF_PROJECT":         "skael-sandboxes",
		"SANDBOX_NF_ALLOWED_DOMAINS": "api.anthropic.com, pypi.org",
		"SANDBOX_NF_NETWORK_POLICY":  "true",
	}))
	if c.Driver != "northflank" || c.NF.Project != "skael-sandboxes" {
		t.Fatalf("config not read: %+v", c)
	}
	// Whitespace around a comma is what an operator actually types.
	want := []string{"api.anthropic.com", "pypi.org"}
	if len(c.NF.AllowedDomains) != 2 || c.NF.AllowedDomains[0] != want[0] || c.NF.AllowedDomains[1] != want[1] {
		t.Errorf("AllowedDomains = %q, want %q with spaces trimmed", c.NF.AllowedDomains, want)
	}
	if !c.NF.NetworkPolicyEnforced {
		t.Error("the enforcement assertion was not read")
	}
	// No bind mounts, so the docker-only host path constraint does not apply.
	if resolve.RequiresHostSharedRoots(c) {
		t.Error("the northflank driver mounts nothing; WORKER_RUN_ROOT must not be required")
	}
}

func TestWarnings_NamesTheUnassertedNorthflankGuarantees(t *testing.T) {
	c := resolve.FromEnv(env(map[string]string{
		"SANDBOX_DRIVER":     "northflank",
		"SANDBOX_NF_TOKEN":   "nf_secret",
		"SANDBOX_NF_PROJECT": "skael-sandboxes",
	}))
	got := strings.Join(c.Warnings(), "\n")
	if !strings.Contains(got, "SANDBOX_NF_NETWORK_POLICY") {
		t.Errorf("warnings = %q, want one naming SANDBOX_NF_NETWORK_POLICY", got)
	}
	if strings.Contains(got, "nf_secret") {
		t.Errorf("warnings leak the API token: %q", got)
	}
}

func TestBuild_RejectsAnUnknownDriverByName(t *testing.T) {
	c := resolve.FromEnv(env(map[string]string{"SANDBOX_DRIVER": "podman"}))
	_, err := c.Build(nil)
	if err == nil || !strings.Contains(err.Error(), "podman") {
		t.Fatalf("Build = %v, want an error naming podman", err)
	}
}

func TestWarnings_SaysWhatIsUnenforcedRatherThanFailingSilently(t *testing.T) {
	c := resolve.FromEnv(env(map[string]string{
		"SANDBOX_DRIVER":        "kubernetes",
		"SANDBOX_K8S_NAMESPACE": "skael-sandbox",
		"SANDBOX_K8S_IMAGE":     "img",
	}))
	got := strings.Join(c.Warnings(), "\n")
	if !strings.Contains(got, "SANDBOX_K8S_NETWORK_POLICY") {
		t.Errorf("warnings = %q, want one naming SANDBOX_K8S_NETWORK_POLICY", got)
	}
}

// An operator who wants the shipped environment must not have to name it. The
// expectation is derived from imagespec, so bumping the base tag cannot leave
// this test asserting the previous environment.
func TestFromEnv_DefaultsTheKubernetesImageToThePublishedBase(t *testing.T) {
	c := resolve.FromEnv(env(map[string]string{
		"SANDBOX_DRIVER":        "kubernetes",
		"SANDBOX_K8S_NAMESPACE": "skael-sandbox",
	}))
	if c.K8s.Image != imagespec.PublishedBaseImage {
		t.Errorf("Image = %q, want the published base %q", c.K8s.Image, imagespec.PublishedBaseImage)
	}
	if err := c.K8s.Validate(); err != nil {
		t.Errorf("a config with no SANDBOX_K8S_IMAGE must still validate: %v", err)
	}
}

func TestFromEnv_AnExplicitKubernetesImageWinsOverTheDefault(t *testing.T) {
	c := resolve.FromEnv(env(map[string]string{
		"SANDBOX_DRIVER":        "kubernetes",
		"SANDBOX_K8S_NAMESPACE": "skael-sandbox",
		"SANDBOX_K8S_IMAGE":     "registry.internal/my-base:7",
	}))
	if c.K8s.Image != "registry.internal/my-base:7" {
		t.Errorf("Image = %q, want the operator's own", c.K8s.Image)
	}
}
