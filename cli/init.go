package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/skael-dev/skael/cli/agents"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/skael-dev/skael/internal/ui"
	"github.com/skael-dev/skael/skills"
	"github.com/spf13/cobra"
)

var skillNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$`)

var initCmd = &cobra.Command{
	Use:   "init [name]",
	Short: "Install the skael and skael-evals skills into this repo, or scaffold a new skill directory",
	Long: `With no argument, write the skael and skael-evals skills to .agents/skills/, and
to .claude/skills/ when Claude Code is detected. A second run changes nothing.
A changed file is refused unless --force is given.

With a name, scaffold a new skill directory.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

var initForce bool

func init() {
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite a changed skael or skael-evals skill")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return initSkaelSkill()
	}
	if initForce {
		return usageError("invalid_flags", "--force applies only with no skill name", "skael init --force")
	}
	name := args[0]

	if !skillNameRe.MatchString(name) {
		return &exitError{
			exit: exitUsage, code: "invalid_name",
			msg:        fmt.Sprintf("invalid skill name %q: must match ^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$", name),
			suggestion: "use lowercase letters, digits, colons, dots, or hyphens",
		}
	}

	dir := name
	if _, err := os.Stat(dir); err == nil {
		return usageError("dir_exists", fmt.Sprintf("directory %q already exists", dir), "")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return failure("mkdir_error", fmt.Sprintf("create directory: %s", err), "")
	}

	content := fmt.Sprintf(`---
name: %s
description: ""
metadata:
  author: ""
  tags: []
  version: "1.0.0"
---

# %s

Describe what this skill does and when an agent should use it.
`, name, name)

	skillMdPath := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(skillMdPath, []byte(content), 0o644); err != nil {
		return failure("write_error", fmt.Sprintf("write SKILL.md: %s", err), "")
	}

	if ui.JSONMode {
		out := struct {
			Path string `json:"path"`
			Name string `json:"name"`
		}{
			Path: skillMdPath,
			Name: name,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	ui.Success("Created %s/SKILL.md — edit the description, then run: skael publish %s", name, name)
	return nil
}

// shipped is one file of a skill this binary carries, at its path under the
// skills directory.
type shipped struct {
	rel  string
	data []byte
}

// shippedSkills lists every file of the skills skael init writes: skael, and
// skael-evals, which writes a suite as a separate job with its own trigger.
func shippedSkills() ([]shipped, error) {
	files := []shipped{{rel: filepath.Join("skael", "SKILL.md"), data: skills.Skael}}
	err := fs.WalkDir(skills.Evals, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(skills.Evals, p)
		if err != nil {
			return err
		}
		files = append(files, shipped{rel: filepath.Join("skael-evals", filepath.FromSlash(p)), data: b})
		return nil
	})
	return files, err
}

// initSkaelSkill writes the shipped skills where agents read skills:
// .agents/skills, and .claude/skills when Claude Code is detected.
func initSkaelSkill() error {
	roots := []string{filepath.Join(".agents", "skills")}
	if home, err := os.UserHomeDir(); err == nil && (&agents.ClaudeCode{}).Detected(home) {
		roots = append(roots, filepath.Join(".claude", "skills"))
	}
	files, err := shippedSkills()
	if err != nil {
		return failure("read_error", fmt.Sprintf("read the shipped skills: %s", err), "")
	}
	type target struct {
		path string
		data []byte
	}
	var targets []target
	statuses := map[string]string{}
	for _, root := range roots {
		for _, f := range files {
			path := filepath.Join(root, f.rel)
			status := "created"
			current, err := os.ReadFile(path)
			switch {
			case err == nil && bytes.Equal(current, f.data):
				status = "unchanged"
			case err == nil && !initForce:
				return usageError("file_changed", path+" differs from the skill this binary carries", "skael init --force")
			case err == nil:
				status = "overwritten"
			case !errors.Is(err, os.ErrNotExist):
				return failure("read_error", fmt.Sprintf("read %s: %s", path, err), "")
			}
			statuses[path] = status
			targets = append(targets, target{path, f.data})
		}
	}
	for _, t := range targets {
		if statuses[t.path] == "unchanged" {
			continue
		}
		if err := writeFileAtomically(t.path, t.data); err != nil {
			return failure("write_error", fmt.Sprintf("write %s: %s", t.path, err), "")
		}
	}

	first := targets[0].path
	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{"path": first, "status": statuses[first], "files": statuses})
	}
	for _, root := range roots {
		for _, name := range []string{"skael", "skael-evals"} {
			dir := filepath.Join(root, name)
			changed := false
			for path, st := range statuses {
				if st != "unchanged" && strings.HasPrefix(path, dir+string(filepath.Separator)) {
					changed = true
				}
			}
			if changed {
				ui.Success("Wrote %s", dir)
			} else {
				ui.Info("%s is up to date", dir)
			}
		}
	}
	return nil
}

func writeFileAtomically(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".SKILL-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
