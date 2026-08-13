package gen_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/skael-dev/skael/cli/whetstone/gen"
	"github.com/skael-dev/skael/internal/eval/lint"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
)

// TestGenerate_DescriptionPromptBudgetComesFromLint pins the fix for the
// guaranteed metadata-token-budget divergence: descriptionPrompt used to
// state an unrelated fixed 1024-byte budget while lint enforced a ~400-byte
// frontmatter total, so every generated skill blew it by construction. The
// budget line must now be derived from lint.MaxMetadataApproxTokens (and
// name length), and 1024 must be gone.
func TestGenerate_DescriptionPromptBudgetComesFromLint(t *testing.T) {
	g := fake.New(scripted()...)
	if _, err := gen.Generate(context.Background(), g, genSpec(), t.TempDir()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	calls := g.Calls()
	descPrompt := calls[len(calls)-1].Prompt

	if strings.Contains(descPrompt, "1024") {
		t.Errorf("description prompt still states the old fixed 1024-byte budget:\n%s", descPrompt)
	}

	// lint.MaxMetadataApproxTokens*4 is the total frontmatter byte budget;
	// the per-skill figure the prompt states is that minus fixed overhead, so
	// it must be strictly smaller.
	totalBudget := lint.MaxMetadataApproxTokens * 4
	if !strings.Contains(descPrompt, "bytes") {
		t.Errorf("description prompt does not state a byte budget:\n%s", descPrompt)
	}
	if strings.Contains(descPrompt, fmt.Sprintf("%d bytes", totalBudget)) {
		t.Errorf("description prompt states the unreduced total budget (%d bytes) instead of subtracting frontmatter overhead", totalBudget)
	}
}
