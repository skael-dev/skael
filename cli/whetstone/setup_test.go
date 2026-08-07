package whetstone_test

import (
	"context"
	"testing"

	whetstone "github.com/skael-dev/skael/cli/whetstone"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// A task that ships a setup script is the ordinary case, not an error: most
// task prompts name an input file, and the setup script is the only thing
// that creates it. This replaces the refusal that used to fire on
// environment/Dockerfile.frag — the field the generator dangled, the model
// filled with workspace setup, and no engine path ever applied — which made
// every generated suite unrunnable at both `suite check` and `eval`.
func TestRunEvalWith_RunsATaskThatShipsASetupScript(t *testing.T) {
	d, req := evalHarness(t)

	suiteDir, err := d.Store.SuiteDir(req.Skill)
	if err != nil {
		t.Fatal(err)
	}
	s, err := suite.Load(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	s.Tasks[0].Setup = "mkdir -p data\nprintf 'a,b\\n' > data/in.csv\n"
	if err := s.Write(suiteDir); err != nil {
		t.Fatal(err)
	}

	// Writing the setup script changes the suite's content ref, and an eval
	// refuses a suite with no check recorded at its ref.
	ref, err := suite.Ref(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]store.SuiteCheckRow, len(s.Tasks))
	for i, task := range s.Tasks {
		rows[i] = store.SuiteCheckRow{TaskID: task.ID}
	}
	if err := d.Store.SaveSuiteCheck(req.Skill, ref, rows); err != nil {
		t.Fatal(err)
	}

	if _, err := whetstone.RunEvalWith(context.Background(), d, req); err != nil {
		t.Fatalf("a task carrying a setup script blocked the eval: %v", err)
	}
}
