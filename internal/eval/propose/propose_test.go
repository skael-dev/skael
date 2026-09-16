package propose_test

import (
	"context"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/propose"
)

func input() propose.Input {
	return propose.Input{
		Skill: "payments:deploy",
		Files: []propose.File{
			{Path: "SKILL.md", Body: "---\nname: payments:deploy\n---\n# deploy\nShip it.\n"},
			{Path: "references/release.md", Body: "Push the branch.\n"},
		},
		Losses: []propose.Loss{
			{TaskID: "task-3", Missed: []string{"tags the release with the version"}, Evidence: "the agent pushed without creating a tag"},
		},
	}
}

// The overfitting defence is that the model cannot read a task. If a task
// prompt ever reaches the prompt, a change can be written to answer it.
func TestPrompt_CarriesTheExpectationsAndNoTask(t *testing.T) {
	p := propose.Prompt(input())

	if !strings.Contains(p, "tags the release with the version") {
		t.Error("the missed expectation did not reach the model")
	}
	if !strings.Contains(p, "the agent pushed without creating a tag") {
		t.Error("the grader's evidence did not reach the model")
	}
	if !strings.Contains(p, "references/release.md") {
		t.Error("the bundle's other prose did not reach the model")
	}
	// A Loss carries a TaskID for the reader. It must not be in the prompt:
	// naming the task is the first step to writing a change about it.
	if strings.Contains(p, "task-3") {
		t.Error("a task id reached the model")
	}
}

func TestPrompt_AsksForADescriptionOnlyWhenThereIsASpec(t *testing.T) {
	if strings.Contains(propose.Prompt(input()), `"description"`) {
		t.Error("a skill with no spec was asked for a description")
	}
	in := input()
	in.Description = "Ship a release the same way every time"
	if !strings.Contains(propose.Prompt(in), "Ship a release the same way every time") {
		t.Error("a skill with a spec was not shown its description")
	}
}

type fakeGateway struct{ reply string }

func (f *fakeGateway) Complete(_ context.Context, _ llm.Req) (llm.Res, error) {
	return llm.Res{Text: f.reply}, nil
}

func (f *fakeGateway) ModelFor(llm.ModelClass) string { return "test-model" }

func TestRun_RefusesACandidateWithNoLoss(t *testing.T) {
	in := input()
	in.Losses = nil
	if _, err := propose.Run(context.Background(), &fakeGateway{}, in); err != propose.ErrNoLosses {
		t.Fatalf("err = %v, want ErrNoLosses", err)
	}
}

// A proposal never adds a file. A generator that can name a new path can write
// a script into a bundle an agent runs.
func TestRun_DropsAFileTheBundleDoesNotHave(t *testing.T) {
	gw := &fakeGateway{reply: `{"changes":[
		{"path":"SKILL.md","body":"new body"},
		{"path":"scripts/deploy.sh","body":"#!/bin/sh\nrm -rf /\n"}],
		"rationale":"tag the release"}`}

	res, err := propose.Run(context.Background(), gw, input())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Changes) != 1 || res.Changes[0].Path != "SKILL.md" {
		t.Fatalf("changes = %+v, want SKILL.md alone", res.Changes)
	}
}

func TestRun_RefusesAProposalThatChangesNothing(t *testing.T) {
	gw := &fakeGateway{reply: `{"changes":[{"path":"nope.md","body":"x"}],"rationale":"none"}`}
	if _, err := propose.Run(context.Background(), gw, input()); err == nil {
		t.Fatal("a proposal touching no known file was accepted")
	}
}
