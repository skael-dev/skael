package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var publishCmd = &cobra.Command{
	Use:   "publish <dir>",
	Short: "Publish a skill to the platform",
	Args:  cobra.ExactArgs(1),
	RunE:  runPublish,
}

var publishForce bool

func init() {
	publishCmd.Flags().BoolVar(&publishForce, "force", false, "Publish even with critical findings")
	rootCmd.AddCommand(publishCmd)
}

func runPublish(cmd *cobra.Command, args []string) error {
	dir := args[0]

	// Read and parse SKILL.md frontmatter for name/description
	skillMdPath := filepath.Join(dir, "SKILL.md")
	data, err := os.ReadFile(skillMdPath)
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError("SKILL.md not found in "+dir, "missing_skill_md", "")
			return nil
		}
		ui.Error(ui.ErrorDetail{
			Message:    "SKILL.md not found in " + dir,
			Suggestion: "create a SKILL.md with name and description frontmatter",
		})
		return nil
	}

	fm, _, err := skill.ParseFrontmatter(string(data))
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError("failed to parse SKILL.md frontmatter: "+err.Error(), "parse_error", "")
			return nil
		}
		ui.Errorf("failed to parse SKILL.md frontmatter: %s", err)
		return nil
	}

	// Resolve name: frontmatter first, then directory basename
	name := ""
	if fm != nil {
		if v, ok := fm["name"]; ok {
			name, _ = v.(string)
		}
	}
	if name == "" {
		name = filepath.Base(dir)
	}

	description := ""
	if fm != nil {
		if v, ok := fm["description"]; ok {
			description, _ = v.(string)
		}
	}

	// Run local security scan and print findings
	report, err := scan.ScanDir(dir)
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "scan_error", "")
			return nil
		}
		ui.Errorf("%s", err)
		return nil
	}

	if !ui.JSONMode {
		if report.Status == "clean" {
			fmt.Fprintln(os.Stdout, "  ✓ No security findings")
		} else {
			for _, f := range report.Findings {
				fmt.Fprintf(os.Stdout, "  %s:%d\t%-10s  %s\n",
					f.File, f.Line, f.Severity, f.Message)
			}
			s := report.Summary
			fmt.Fprintf(os.Stdout, "\n  %d critical · %d high · %d medium · %d info\n",
				s.Critical, s.High, s.Medium, s.Info)
		}
	}

	// Block on critical findings unless --force
	if report.Status == "critical" && !publishForce {
		if ui.JSONMode {
			ui.PrintJSONError("critical security findings block publish", "scan_blocked", "skael publish --force")
			return nil
		}
		ui.Error(ui.ErrorDetail{
			Message:    "critical security findings block publish",
			Suggestion: "skael publish --force",
		})
		return nil
	}

	// Pack the skill directory into a tar.gz archive
	archive, _, entries, err := skill.Pack(dir)
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "pack_error", "")
			return nil
		}
		ui.Errorf("%s", err)
		return nil
	}

	sizekb := float64(len(archive)) / 1024.0
	if !ui.JSONMode {
		fmt.Fprintf(os.Stdout, "  ✓ Packed %s (%d files, %.1f KB)\n", name, len(entries), sizekb)
	}

	// Load config and create API client
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

	// Check if skill exists, create if not
	existing, err := c.GetSkill(name)
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "api_error", "")
			return nil
		}
		ui.Errorf("%s", err)
		return nil
	}
	if existing == nil {
		_, err = c.CreateSkill(name, description)
		if err != nil {
			if ui.JSONMode {
				ui.PrintJSONError(err.Error(), "api_error", "")
				return nil
			}
			ui.Errorf("%s", err)
			return nil
		}
	}

	// Publish the new version
	ver, _, pubErr := c.PublishVersion(name, archive)
	if pubErr != nil {
		if apiErr, ok := pubErr.(*client.APIError); ok && apiErr.StatusCode == http.StatusUnprocessableEntity {
			if ui.JSONMode {
				ui.PrintJSONError("publish blocked by server-side security scan", "scan_blocked", "skael publish --force")
				return nil
			}
			ui.Error(ui.ErrorDetail{
				Message:    "publish blocked by server-side security scan",
				Suggestion: "skael publish --force",
			})
			return nil
		}
		if ui.JSONMode {
			ui.PrintJSONError(pubErr.Error(), "api_error", "")
			return nil
		}
		ui.Errorf("%s", pubErr)
		return nil
	}

	if ui.JSONMode {
		out := struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
		}{
			Name:    name,
			Version: ver.Version,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	fmt.Fprintf(os.Stdout, "  ✓ Published v%d\n", ver.Version)
	fmt.Fprintf(os.Stdout, "  %s/skills/%s\n", cfg.Endpoint, name)
	return nil
}
