package agent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/internal/eval/agent"
)

// InstallSkill must install shipped content only. The directory it is handed
// is the authoring skill dir, which also holds the eval sidecar — a real
// report proved the reference solution was reaching the sandbox by listing
// ".../oracle/solve.sh" among the files the agent under test had observed.
func TestInstallSkill_DoesNotInstallTheEvalSidecar(t *testing.T) {
	src := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Shipped content.
	write("SKILL.md", "---\nname: demo\n---\nbody\n")
	write("scripts/do.py", "print('hi')\n")
	write("references/style.md", "style\n")
	// Authoring scaffolding that must not ship.
	write("spec.yaml", "name: demo\n")
	write("eval/contract.yaml", "version: 1\n")
	write("eval/suite/tasks/happy/oracle/solve.sh", "#!/bin/sh\necho the answer\n")
	write("eval/suite/tasks/happy/verifier/test.sh", "#!/bin/sh\nexit 0\n")

	ws := t.TempDir()
	a := agent.New()
	if err := a.InstallSkill(ws, src); err != nil {
		t.Fatal(err)
	}

	installed := filepath.Join(ws, a.Caps().SkillDir, filepath.Base(src))

	for _, rel := range []string{"SKILL.md", "scripts/do.py", "references/style.md"} {
		if _, err := os.Stat(filepath.Join(installed, filepath.FromSlash(rel))); err != nil {
			t.Errorf("shipped content %s was not installed: %v", rel, err)
		}
	}

	for _, rel := range []string{
		"eval",
		"eval/contract.yaml",
		"eval/suite/tasks/happy/oracle/solve.sh",
		"eval/suite/tasks/happy/verifier/test.sh",
		"spec.yaml",
	} {
		if _, err := os.Stat(filepath.Join(installed, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s was installed into the agent's workspace; the agent being measured "+
				"can read it", rel)
		}
	}
}
