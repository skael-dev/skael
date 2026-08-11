package spec

import (
	"strings"
	"testing"
)

// TestDraftPrompt_AsksForEnoughTriggerPhrases pins the counts the eval path
// needs. runner.BuildPlan needs three positive and three negative queries at
// the full tier. It needs eight of each at the deep tier. The shipped
// default in tune reads sixteen. A prompt that states no count produced
// specs with two positives, which cannot be scored at their own default
// tier.
func TestDraftPrompt_AsksForEnoughTriggerPhrases(t *testing.T) {
	for _, want := range []string{"8 positive", "8 hard negative"} {
		if !strings.Contains(draftPrompt, want) {
			t.Errorf("draftPrompt does not ask for %q", want)
		}
	}
}
