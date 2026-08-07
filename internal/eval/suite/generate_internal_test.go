package suite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/spec"
)

func genSpec() *spec.SkillSpec {
	return &spec.SkillSpec{
		Name:        "csv-to-markdown",
		Purpose:     "convert csv to markdown tables",
		Description: "a skill under test",
	}
}

// outlineJSON builds an outline response with n stubs.
func outlineJSON(n int) string {
	type stub struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Intent string `json:"intent"`
	}
	out := struct {
		Tasks    []stub `json:"tasks"`
		Triggers struct {
			Positive []string `json:"positive"`
			Negative []string `json:"negative"`
		} `json:"triggers"`
	}{}
	for i := 0; i < n; i++ {
		out.Tasks = append(out.Tasks, stub{
			ID:     "task-" + string(rune('a'+i)),
			Kind:   "edge",
			Intent: "do the thing",
		})
	}
	out.Triggers.Positive = []string{"convert this csv"}
	out.Triggers.Negative = []string{"convert this pdf"}
	b, _ := json.Marshal(out)
	return string(b)
}

const expandJSON = `{"prompt_md":"p","setup":"echo setup","oracle":"echo solve","verifier":"echo test"}`

// route answers the outline call with outline and every expansion with expand,
// except stubs whose id is in fail, which error.
func route(outline string, fail map[string]bool) func(llm.Req) (string, error) {
	return func(r llm.Req) (string, error) {
		if r.Role == roleOutline {
			return outline, nil
		}
		for id := range fail {
			if strings.Contains(r.Prompt, id) {
				return "", errors.New("expansion failed for " + id)
			}
		}
		return expandJSON, nil
	}
}

func TestGenerateN_ExpandsEveryStubIntoATask(t *testing.T) {
	g := fake.NewFunc(route(outlineJSON(3), nil))

	s, dropped, err := GenerateN(context.Background(), g, genSpec(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tasks) != 3 {
		t.Errorf("got %d tasks, want 3", len(s.Tasks))
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %+v, want none", dropped)
	}
	for _, task := range s.Tasks {
		if task.Verifier == "" || task.Oracle == "" || task.PromptMD == "" {
			t.Errorf("task %s came back incomplete: %+v", task.ID, task)
		}
	}
	if len(s.Triggers.Positive) == 0 || len(s.Triggers.Negative) == 0 {
		t.Error("the outline's trigger set did not survive")
	}
}

// The whole point of the rework: one bad expansion costs one task, not the run.
func TestGenerateN_AFailedExpansionDropsOnlyThatTask(t *testing.T) {
	g := fake.NewFunc(route(outlineJSON(3), map[string]bool{"task-b": true}))

	s, dropped, err := GenerateN(context.Background(), g, genSpec(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tasks) != 2 {
		t.Errorf("got %d tasks, want 2", len(s.Tasks))
	}
	for _, task := range s.Tasks {
		if task.ID == "task-b" {
			t.Error("the failed task was kept")
		}
	}
	if len(dropped) != 1 || dropped[0].TaskID != "task-b" {
		t.Fatalf("dropped = %+v, want exactly task-b", dropped)
	}
	if dropped[0].Reason == "" {
		t.Error("a dropped task carries no reason")
	}
}

func TestGenerateN_AllExpansionsFailingIsAnError(t *testing.T) {
	g := fake.NewFunc(route(outlineJSON(2), map[string]bool{"task-a": true, "task-b": true}))

	if _, _, err := GenerateN(context.Background(), g, genSpec(), 2); err == nil {
		t.Error("a suite with no tasks was returned as a success")
	}
}

// The outline is bounded by construction, so its failure is a real gateway
// problem and there is nothing to fan out from.
func TestGenerateN_AFailedOutlineAbortsTheDraft(t *testing.T) {
	g := fake.NewFunc(func(llm.Req) (string, error) { return "", errors.New("gateway down") })

	if _, _, err := GenerateN(context.Background(), g, genSpec(), 3); err == nil {
		t.Error("a failed outline did not abort the draft")
	}
}

func TestGenerateN_MakesOneOutlineCallAndOnePerStub(t *testing.T) {
	g := fake.NewFunc(route(outlineJSON(4), nil))

	if _, _, err := GenerateN(context.Background(), g, genSpec(), 4); err != nil {
		t.Fatal(err)
	}

	var outlines, expansions int
	for _, c := range g.Calls() {
		switch c.Role {
		case roleOutline:
			outlines++
		case roleExpand:
			expansions++
		}
	}
	if outlines != 1 {
		t.Errorf("outline calls = %d, want 1", outlines)
	}
	if expansions != 4 {
		t.Errorf("expansion calls = %d, want 4", expansions)
	}
}
