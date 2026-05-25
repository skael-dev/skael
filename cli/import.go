package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var importCmd = &cobra.Command{
	Use:   "import <url|path>",
	Short: "Import skills from GitHub, local directory, or skills.sh",
	Long: `Import skills into the Skael registry from external sources.

Examples:
  skael import https://github.com/anthropics/skills
  skael import https://github.com/anthropics/skills/tree/main/skills/docx
  skael import ./my-skills/code-review
  skael import --search "react testing"`,
	Args: cobra.MaximumNArgs(1),
	RunE: runImport,
}

var (
	importAll    bool
	importDryRun bool
	importSearch string
)

func init() {
	importCmd.Flags().BoolVar(&importAll, "all", false, "Import all discovered skills without prompting")
	importCmd.Flags().BoolVar(&importDryRun, "dry-run", false, "Preview without importing")
	importCmd.Flags().StringVar(&importSearch, "search", "", "Search skills.sh and import from results")
	rootCmd.AddCommand(importCmd)
}

var (
	importHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#ededed"))

	importSourceStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#a0a0a0"))

	importNameStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#22c55e")).
			Bold(true)

	importDescStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#a0a0a0"))

	importFilesStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666"))

	importBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#333333")).
			Padding(0, 1)

	scanCleanStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#22c55e"))

	scanWarnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f59e0b"))

	scanCriticalStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ef4444"))
)

func runImport(cmd *cobra.Command, args []string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError("not configured", "not_configured", "skael setup <url> <api-key>")
			return nil
		}
		ui.Error(ui.ErrorDetail{
			Message:    "not configured",
			Suggestion: "skael setup <url> <api-key>",
		})
		return nil
	}
	c := client.New(cfg.Endpoint, cfg.APIKey)

	if importSearch != "" {
		return runSearchImport(c, importSearch)
	}

	if len(args) == 0 {
		return fmt.Errorf("provide a URL or local path, or use --search")
	}

	input := args[0]

	if isLocalPath(input) {
		return runLocalImport(c, input)
	}

	return runURLImport(c, input)
}

func runURLImport(c *client.Client, rawURL string) error {
	if !ui.JSONMode {
		fmt.Fprintf(os.Stdout, "\n  %s Resolving %s...\n", ui.Accent("↓"), rawURL)
	}

	resolved, err := c.ImportResolve(rawURL)
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "resolve_error", "")
			return nil
		}
		ui.Errorf("%s", err)
		return nil
	}

	if len(resolved.Skills) == 0 {
		if ui.JSONMode {
			ui.PrintJSONError("no skills found", "no_skills", "")
			return nil
		}
		ui.Warn("No skills found at %s", rawURL)
		return nil
	}

	return presentAndImport(c, resolved)
}

func runLocalImport(c *client.Client, path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		ui.Errorf("invalid path: %s", err)
		return nil
	}

	if !ui.JSONMode {
		fmt.Fprintf(os.Stdout, "\n  %s Packing %s...\n", ui.Accent("↓"), absPath)
	}

	var dirs []string
	if _, statErr := os.Stat(filepath.Join(absPath, "SKILL.md")); statErr == nil {
		dirs = []string{absPath}
	} else {
		filepath.Walk(absPath, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if info.Name() == "SKILL.md" {
				dirs = append(dirs, filepath.Dir(p))
			}
			return nil
		})
	}

	if len(dirs) == 0 {
		ui.Warn("No skills found in %s", path)
		return nil
	}

	for _, dir := range dirs {
		archive, _, _, err := skill.Pack(dir)
		if err != nil {
			ui.Errorf("pack %s: %s", dir, err)
			continue
		}

		resolved, err := c.ImportUpload(archive)
		if err != nil {
			ui.Errorf("upload %s: %s", dir, err)
			continue
		}

		if len(resolved.Skills) == 0 {
			continue
		}

		names := make([]string, len(resolved.Skills))
		for i, s := range resolved.Skills {
			names[i] = s.Name
		}

		result, err := c.ImportSkills(resolved.Source, names)
		if err != nil {
			ui.Errorf("import: %s", err)
			continue
		}

		for _, imp := range result.Imported {
			ui.Success("%s v%d imported", imp.Name, imp.Version)
		}
		for _, fail := range result.Failed {
			ui.Errorf("%s: %s", fail.Name, fail.Error)
		}
	}

	return nil
}

func presentAndImport(c *client.Client, resolved *client.ResolveResponse) error {
	src := resolved.Source
	sourceLabel := fmt.Sprintf("%s/%s", src.Owner, src.Repo)
	refLabel := src.Ref
	if refLabel == "" {
		refLabel = "default"
	}
	shaShort := src.CommitSHA
	if len(shaShort) > 7 {
		shaShort = shaShort[:7]
	}

	if ui.JSONMode {
		if importDryRun {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(resolved)
		}
		names := make([]string, len(resolved.Skills))
		for i, s := range resolved.Skills {
			names[i] = s.Name
		}
		result, err := c.ImportSkills(src, names)
		if err != nil {
			ui.PrintJSONError(err.Error(), "import_error", "")
			return nil
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Header
	fmt.Fprintf(os.Stdout, "\n  %s %s (%s @ %s)\n\n",
		importHeaderStyle.Render("Import ·"),
		importSourceStyle.Render(sourceLabel),
		importSourceStyle.Render(refLabel),
		importSourceStyle.Render(shaShort),
	)

	// Build skill rows
	var rows []string
	for _, sk := range resolved.Skills {
		check := "[ ]"
		if importAll {
			check = "[x]"
		}

		scanBadge := scanCleanStyle.Render("clean")
		if sk.ScanStatus == "warn" {
			scanBadge = scanWarnStyle.Render("warn")
		} else if sk.ScanStatus == "critical" {
			scanBadge = scanCriticalStyle.Render("critical")
		}

		name := importNameStyle.Render(fmt.Sprintf("%-20s", sk.Name))
		desc := importDescStyle.Render(truncateDesc(sk.Description, 35))
		files := importFilesStyle.Render(fmt.Sprintf("%d files", len(sk.Files)))

		row := fmt.Sprintf("  %s  %s %s  %s  %s", check, name, desc, files, scanBadge)
		rows = append(rows, row)
	}

	fmt.Fprintln(os.Stdout, importBoxStyle.Render(strings.Join(rows, "\n")))

	if importDryRun {
		fmt.Fprintf(os.Stdout, "\n  %s\n\n", importSourceStyle.Render("(dry run — no changes made)"))
		return nil
	}

	// Selection prompt
	selected := resolved.Skills
	if !importAll {
		fmt.Fprintf(os.Stdout, "\n  %d skills available\n", len(resolved.Skills))
		fmt.Fprintf(os.Stdout, "  Import all? [y/N] ")

		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(os.Stdout, "  Cancelled.")
			return nil
		}
	}

	// Execute import
	names := make([]string, len(selected))
	for i, s := range selected {
		names[i] = s.Name
	}

	fmt.Fprintf(os.Stdout, "\n  %s Importing %d skills...\n", ui.Accent("↓"), len(names))

	result, err := c.ImportSkills(resolved.Source, names)
	if err != nil {
		ui.Errorf("%s", err)
		return nil
	}

	fmt.Fprintln(os.Stdout)
	for _, imp := range result.Imported {
		ui.Success("%s v%d", imp.Name, imp.Version)
	}
	for _, fail := range result.Failed {
		ui.Errorf("%s: %s", fail.Name, fail.Error)
	}

	parts := []string{fmt.Sprintf("%d imported", len(result.Imported))}
	if len(result.Failed) > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", len(result.Failed)))
	}
	ui.Summary(parts...)

	return nil
}

func runSearchImport(c *client.Client, query string) error {
	ui.Warn("skills.sh search integration is not yet implemented")
	ui.Info("Use a GitHub URL directly: skael import https://github.com/owner/repo")
	return nil
}

func isLocalPath(s string) bool {
	if strings.HasPrefix(s, "./") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "../") {
		return true
	}
	if _, err := os.Stat(s); err == nil {
		return true
	}
	return false
}

func truncateDesc(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
