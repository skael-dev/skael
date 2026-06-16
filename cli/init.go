package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var skillNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$`)

var initCmd = &cobra.Command{
	Use:   "init <name>",
	Short: "Scaffold a new skill directory",
	Args:  cobra.ExactArgs(1),
	RunE:  runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	name := args[0]

	if !skillNameRe.MatchString(name) {
		if ui.JSONMode {
			ui.PrintJSONError(
				fmt.Sprintf("invalid skill name %q: must match ^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$", name),
				"invalid_name",
				"use lowercase letters, digits, colons, dots, or hyphens",
			)
			return nil
		}
		ui.Error(ui.ErrorDetail{
			Message:    fmt.Sprintf("invalid skill name %q", name),
			Context:    "must match ^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$",
			Suggestion: "use lowercase letters, digits, colons, dots, or hyphens",
		})
		return fmt.Errorf("invalid skill name")
	}

	dir := name
	if _, err := os.Stat(dir); err == nil {
		if ui.JSONMode {
			ui.PrintJSONError(fmt.Sprintf("directory %q already exists", dir), "dir_exists", "")
			return nil
		}
		ui.Errorf("directory %q already exists", dir)
		return fmt.Errorf("directory already exists")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "mkdir_error", "")
			return nil
		}
		ui.Errorf("create directory: %s", err)
		return fmt.Errorf("create directory: %w", err)
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
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "write_error", "")
			return nil
		}
		ui.Errorf("write SKILL.md: %s", err)
		return fmt.Errorf("write SKILL.md: %w", err)
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
