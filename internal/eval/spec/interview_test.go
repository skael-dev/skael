package spec_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/spec"
)

func mustJSON(t *testing.T, s *spec.SkillSpec) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInterview_ProducesAValidSpec(t *testing.T) {
	draft := fixture()
	final := fixture()
	final.Purpose = "Refined by the critique pass."

	g := fake.New(mustJSON(t, draft), mustJSON(t, final))

	got, err := spec.Interview(context.Background(), g, "I want a skill that pulls tables out of PDFs")
	if err != nil {
		t.Fatalf("Interview: %v", err)
	}
	if got.Purpose != "Refined by the critique pass." {
		t.Errorf("Interview returned the draft, not the critiqued spec: %q", got.Purpose)
	}
	if errs := got.Validate(); len(errs) != 0 {
		t.Errorf("Interview returned an invalid spec: %v", errs)
	}
	if n := len(g.Calls()); n != 2 {
		t.Errorf("made %d gateway calls, want 2 (draft + critique)", n)
	}
}

func TestInterview_PassesTheIntentToTheModel(t *testing.T) {
	g := fake.New(mustJSON(t, fixture()), mustJSON(t, fixture()))
	const intent = "pull tables out of quarterly PDFs"

	if _, err := spec.Interview(context.Background(), g, intent); err != nil {
		t.Fatalf("Interview: %v", err)
	}
	if !strings.Contains(g.Calls()[0].Prompt, intent) {
		t.Error("the draft prompt does not contain the user's intent")
	}
}

func TestInterview_CritiqueSeesTheDraftAndItsValidationErrors(t *testing.T) {
	// A draft missing postconditions must have those specific errors quoted into
	// the critique prompt. Without them the second call is a re-roll rather than
	// a repair, and it will reproduce the same defect.
	broken := fixture()
	broken.Steps[0].Postcondition = ""
	broken.Steps[1].Postcondition = ""

	g := fake.New(mustJSON(t, broken), mustJSON(t, fixture()))

	if _, err := spec.Interview(context.Background(), g, "x"); err != nil {
		t.Fatalf("Interview: %v", err)
	}

	critique := g.Calls()[1].Prompt
	if !strings.Contains(critique, "postcondition") {
		t.Errorf("critique prompt does not quote the validation errors:\n%s", critique)
	}
	if !strings.Contains(critique, broken.Purpose) {
		t.Error("critique prompt does not include the draft")
	}
}

func TestInterview_RejectsAnEmptyIntent(t *testing.T) {
	g := fake.New()
	if _, err := spec.Interview(context.Background(), g, "   "); err != spec.ErrEmptyIntent {
		t.Errorf("err = %v, want ErrEmptyIntent", err)
	}
	if len(g.Calls()) != 0 {
		t.Error("Interview called the gateway with an empty intent")
	}
}

func TestInterview_StillFailsIfTheCritiquedSpecIsInvalid(t *testing.T) {
	// Two rounds is the budget. If the result is still invalid the caller must
	// hear about it rather than receive a spec that cannot be generated from.
	broken := fixture()
	broken.Steps = nil

	g := fake.New(mustJSON(t, broken), mustJSON(t, broken))

	if _, err := spec.Interview(context.Background(), g, "x"); err == nil {
		t.Fatal("Interview returned an invalid spec without error")
	}
}

func TestInterview_RequestsJSONSchema(t *testing.T) {
	g := fake.New(mustJSON(t, fixture()), mustJSON(t, fixture()))
	if _, err := spec.Interview(context.Background(), g, "x"); err != nil {
		t.Fatal(err)
	}
	for i, c := range g.Calls() {
		if len(c.Schema) == 0 {
			t.Errorf("call %d carries no schema; the gateway constrains output with it", i)
		}
		if c.ModelClass != "" && c.ModelClass != "strong" {
			t.Errorf("call %d uses model class %q; authoring should use the strong class", i, c.ModelClass)
		}
	}
}
