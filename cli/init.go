package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/skael-dev/skael/cli/agents"
	"os"
	"path/filepath"
	"regexp"

	"github.com/skael-dev/skael/internal/ui"
	"github.com/skael-dev/skael/skills"
	"github.com/spf13/cobra"
)

var skillNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$`)

var initCmd = &cobra.Command{
	Use:   "init [name]",
	Short: "Install the skael skill into this repo, or scaffold a new skill directory",
	Long: `With no argument, write the skael skill to .agents/skills/skael/SKILL.md, and
to .claude/skills/skael/SKILL.md when Claude Code is detected. A
second run changes nothing. A changed file is refused unless --force is given.

With a name, scaffold a new skill directory.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

var initForce bool

func init() {
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite a changed skael skill")
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

// initSkaelSkill writes the skael skill where agents read skills:
// .agents/skills, and .claude/skills when Claude Code is detected.
func initSkaelSkill() error {
	paths := []string{filepath.Join(".agents", "skills", "skael", "SKILL.md")}
	if home, err := os.UserHomeDir(); err == nil && (&agents.ClaudeCode{}).Detected(home) {
		paths = append(paths, filepath.Join(".claude", "skills", "skael", "SKILL.md"))
	}
	statuses := map[string]string{}
	for _, path := range paths {
		status := "created"
		current, err := os.ReadFile(path)
		switch {
		case err == nil && bytes.Equal(current, skills.Skael):
			status = "unchanged"
		case err == nil && !initForce:
			return usageError("file_changed", path+" differs from the skill this binary carries", "skael init --force")
		case err == nil:
			status = "overwritten"
		case !errors.Is(err, os.ErrNotExist):
			return failure("read_error", fmt.Sprintf("read %s: %s", path, err), "")
		}
		statuses[path] = status
	}
	for _, path := range paths {
		if statuses[path] == "unchanged" {
			continue
		}
		if err := writeFileAtomically(path, skills.Skael); err != nil {
			return failure("write_error", fmt.Sprintf("write %s: %s", path, err), "")
		}
	}

	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{"path": paths[0], "status": statuses[paths[0]], "files": statuses})
	}
	for _, path := range paths {
		if statuses[path] == "unchanged" {
			ui.Info("%s is up to date", path)
		} else {
			ui.Success("Wrote %s", path)
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
