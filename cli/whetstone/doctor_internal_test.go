package whetstone

import (
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/sandbox/resolve"
)

// doctor exists so a setup problem is found before a score is, so the resolved
// driver and every unasserted guarantee must appear in its output.
func TestDoctor_ReportsTheResolvedDriverAndItsWarnings(t *testing.T) {
	out := doctorReport(resolve.FromEnv(func(k string) string {
		switch k {
		case "SANDBOX_DRIVER":
			return "kubernetes"
		case "SANDBOX_K8S_NAMESPACE":
			return "skael-sandbox"
		case "SANDBOX_K8S_IMAGE":
			return "img"
		}
		return ""
	}))
	for _, want := range []string{"kubernetes", "skael-sandbox", "SANDBOX_K8S_NETWORK_POLICY"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output does not mention %q:\n%s", want, out)
		}
	}
}
