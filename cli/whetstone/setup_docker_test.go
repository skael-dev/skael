//go:build docker

package whetstone_test

import (
	"context"
	"os"
	"testing"

	whetstone "github.com/skael-dev/skael/cli/whetstone"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// The end-to-end proof that a setup script actually runs, in a real sandbox:
// the task's oracle reads a file that nothing but the setup script creates,
// so an unapplied setup script fails the oracle and voids the task. Before
// this, `suite check` refused the suite outright rather than running it.
func TestRunSuiteCheck_RunsATaskSetupScript(t *testing.T) {
	root := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	st, err := store.Open(root)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	seedSkill(t, st, "demo", 5, nil)
	suiteDir, err := st.SuiteDir("demo")
	if err != nil {
		t.Fatal(err)
	}
	s, err := suite.Load(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range s.Tasks {
		// Every task must be well-formed or `suite check` exits non-zero for
		// a reason that has nothing to do with setup: the oracle solves it,
		// and the verifier rejects a workspace the oracle has not touched.
		s.Tasks[i].Oracle = "#!/bin/sh\nset -e\ncp data/in.csv out.txt\n"
		s.Tasks[i].Verifier = "#!/bin/sh\ntest -f out.txt\n"
		s.Tasks[i].Setup = "#!/bin/sh\nset -e\nmkdir -p data\nprintf 'a,b\\n' > data/in.csv\n"
	}
	if err := s.Write(suiteDir); err != nil {
		t.Fatal(err)
	}

	if err := whetstone.RunSuiteCheck(context.Background(), "demo", false); err != nil {
		t.Fatalf("a task whose inputs come from its setup script did not clear the gate: %v", err)
	}
}
