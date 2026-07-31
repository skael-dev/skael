package suite

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Permissions for files written into a suite directory. Oracle and verifier
// scripts are executed directly by a later sandbox stage, so they must be
// executable; everything else is not.
const (
	scriptMode = os.FileMode(0o755)
	fileMode   = os.FileMode(0o644)
	dirMode    = os.FileMode(0o755)
)

// taskMeta is the tasks/<id>/meta.yaml sidecar: the fields Write assigns or
// receives that don't have a natural home among task.md/oracle/verifier, so
// Load can restore them.
type taskMeta struct {
	Kind  string `yaml:"kind"`
	Split string `yaml:"split"`
}

// safeJoin resolves id inside tasksDir, refusing anything that escapes it.
// Task IDs are model-authored and become directory names, so they are
// untrusted input: an absolute ID or a traversal must be an error rather
// than something quietly cleaned up. Same guard shape as internal/eval/gen's
// safeJoin.
func safeJoin(tasksDir, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("suite: empty task id")
	}
	if filepath.IsAbs(id) {
		return "", fmt.Errorf("suite: absolute task id %q", id)
	}
	target := filepath.Join(tasksDir, id)
	within, err := filepath.Rel(tasksDir, target)
	if err != nil {
		return "", fmt.Errorf("suite: resolving task id %q: %w", id, err)
	}
	if within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("suite: task id %q escapes the suite directory", id)
	}
	return target, nil
}

// Write writes the suite to dir in the SkillsBench-compatible layout:
//
//	dir/
//	├── triggers.yaml
//	└── tasks/<id>/
//	    ├── task.md
//	    ├── meta.yaml
//	    ├── environment/Dockerfile.frag   (only when EnvFrag is non-empty)
//	    ├── oracle/solve.sh               (mode 0755)
//	    └── verifier/test.sh              (mode 0755)
func (s *Suite) Write(dir string) error {
	tasksDir := filepath.Join(dir, "tasks")
	if err := os.MkdirAll(tasksDir, dirMode); err != nil {
		return fmt.Errorf("suite: creating tasks directory: %w", err)
	}

	for _, task := range s.Tasks {
		taskDir, err := safeJoin(tasksDir, task.ID)
		if err != nil {
			return fmt.Errorf("suite: writing task: %w", err)
		}
		if err := writeTask(taskDir, task); err != nil {
			return err
		}
	}

	return writeTriggers(dir, s.Triggers)
}

// writeTask writes one task package under taskDir, which safeJoin has
// already confirmed sits inside the suite's tasks directory.
func writeTask(taskDir string, task TaskPkg) error {
	if err := os.MkdirAll(taskDir, dirMode); err != nil {
		return fmt.Errorf("suite: creating task directory for %q: %w", task.ID, err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "task.md"), []byte(task.PromptMD), fileMode); err != nil {
		return fmt.Errorf("suite: writing task.md for %q: %w", task.ID, err)
	}

	if task.EnvFrag != "" {
		envDir := filepath.Join(taskDir, "environment")
		if err := os.MkdirAll(envDir, dirMode); err != nil {
			return fmt.Errorf("suite: creating environment directory for %q: %w", task.ID, err)
		}
		if err := os.WriteFile(filepath.Join(envDir, "Dockerfile.frag"), []byte(task.EnvFrag), fileMode); err != nil {
			return fmt.Errorf("suite: writing environment fragment for %q: %w", task.ID, err)
		}
	}

	oracleDir := filepath.Join(taskDir, "oracle")
	if err := os.MkdirAll(oracleDir, dirMode); err != nil {
		return fmt.Errorf("suite: creating oracle directory for %q: %w", task.ID, err)
	}
	if err := os.WriteFile(filepath.Join(oracleDir, "solve.sh"), []byte(task.Oracle), scriptMode); err != nil {
		return fmt.Errorf("suite: writing oracle for %q: %w", task.ID, err)
	}

	verifierDir := filepath.Join(taskDir, "verifier")
	if err := os.MkdirAll(verifierDir, dirMode); err != nil {
		return fmt.Errorf("suite: creating verifier directory for %q: %w", task.ID, err)
	}
	if err := os.WriteFile(filepath.Join(verifierDir, "test.sh"), []byte(task.Verifier), scriptMode); err != nil {
		return fmt.Errorf("suite: writing verifier for %q: %w", task.ID, err)
	}

	meta := taskMeta{Kind: task.Kind, Split: task.Split}
	metaBytes, err := yaml.Marshal(meta)
	if err != nil {
		return fmt.Errorf("suite: marshalling meta for %q: %w", task.ID, err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "meta.yaml"), metaBytes, fileMode); err != nil {
		return fmt.Errorf("suite: writing meta for %q: %w", task.ID, err)
	}
	return nil
}

// writeTriggers writes the suite-level trigger set.
func writeTriggers(dir string, t TriggerSet) error {
	b, err := yaml.Marshal(t)
	if err != nil {
		return fmt.Errorf("suite: marshalling triggers: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "triggers.yaml"), b, fileMode); err != nil {
		return fmt.Errorf("suite: writing triggers.yaml: %w", err)
	}
	return nil
}

// Load reads a suite previously written by Write back from dir.
func Load(dir string) (*Suite, error) {
	triggers, err := loadTriggers(dir)
	if err != nil {
		return nil, err
	}

	tasksDir := filepath.Join(dir, "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return nil, fmt.Errorf("suite: reading tasks directory: %w", err)
	}

	tasks := make([]TaskPkg, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		task, err := loadTask(tasksDir, e.Name())
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}

	return &Suite{Tasks: tasks, Triggers: triggers}, nil
}

// loadTask reads one task package back from tasksDir/id.
func loadTask(tasksDir, id string) (TaskPkg, error) {
	taskDir := filepath.Join(tasksDir, id)

	promptMD, err := os.ReadFile(filepath.Join(taskDir, "task.md"))
	if err != nil {
		return TaskPkg{}, fmt.Errorf("suite: reading task.md for %q: %w", id, err)
	}

	oracle, err := os.ReadFile(filepath.Join(taskDir, "oracle", "solve.sh"))
	if err != nil {
		return TaskPkg{}, fmt.Errorf("suite: reading oracle for %q: %w", id, err)
	}

	verifier, err := os.ReadFile(filepath.Join(taskDir, "verifier", "test.sh"))
	if err != nil {
		return TaskPkg{}, fmt.Errorf("suite: reading verifier for %q: %w", id, err)
	}

	var envFrag string
	if b, err := os.ReadFile(filepath.Join(taskDir, "environment", "Dockerfile.frag")); err == nil {
		envFrag = string(b)
	} else if !os.IsNotExist(err) {
		return TaskPkg{}, fmt.Errorf("suite: reading environment fragment for %q: %w", id, err)
	}

	metaBytes, err := os.ReadFile(filepath.Join(taskDir, "meta.yaml"))
	if err != nil {
		return TaskPkg{}, fmt.Errorf("suite: reading meta for %q: %w", id, err)
	}
	var meta taskMeta
	if err := yaml.Unmarshal(metaBytes, &meta); err != nil {
		return TaskPkg{}, fmt.Errorf("suite: parsing meta for %q: %w", id, err)
	}

	return TaskPkg{
		ID:       id,
		Kind:     meta.Kind,
		Split:    meta.Split,
		PromptMD: string(promptMD),
		EnvFrag:  envFrag,
		Oracle:   string(oracle),
		Verifier: string(verifier),
	}, nil
}

// loadTriggers reads the suite-level trigger set.
func loadTriggers(dir string) (TriggerSet, error) {
	b, err := os.ReadFile(filepath.Join(dir, "triggers.yaml"))
	if err != nil {
		return TriggerSet{}, fmt.Errorf("suite: reading triggers.yaml: %w", err)
	}
	var t TriggerSet
	if err := yaml.Unmarshal(b, &t); err != nil {
		return TriggerSet{}, fmt.Errorf("suite: parsing triggers.yaml: %w", err)
	}
	return t, nil
}
