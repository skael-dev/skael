package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all skills on the platform",
	RunE:  runList,
}

var listInstalled bool

func init() {
	listCmd.Flags().BoolVar(&listInstalled, "installed", false, "Show only installed skills with their scope")
	rootCmd.AddCommand(listCmd)
}

func runList(cmd *cobra.Command, args []string) error {
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

	if listInstalled {
		return runListInstalled(cfg)
	}

	c := client.New(cfg.Endpoint, cfg.APIKey)

	var allSkills []client.Skill
	var total int
	offset := 0
	for {
		page, pageTotal, err := c.ListSkills(100, offset)
		if err != nil {
			if ui.JSONMode {
				ui.PrintJSONError(err.Error(), "api_error", "")
				return nil
			}
			ui.Errorf("%s", err)
			return nil
		}
		total = pageTotal
		allSkills = append(allSkills, page...)
		if len(allSkills) >= total || len(page) == 0 {
			break
		}
		offset += len(page)
	}
	skills := allSkills

	if ui.JSONMode {
		out := struct {
			Skills interface{} `json:"skills"`
			Total  int         `json:"total"`
		}{
			Skills: skills,
			Total:  total,
		}
		return ui.PrintJSON(out)
	}

	if len(skills) == 0 {
		fmt.Fprintln(os.Stdout, "  No skills published yet.")
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "  Try: skael publish ./my-skill")
		return nil
	}

	for _, sk := range skills {
		age := formatAge(sk.UpdatedAt)
		desc := truncate(sk.Description, 40)
		line := fmt.Sprintf("  %-24s v%d · %-8s  %s",
			sk.Name,
			sk.LatestVersion,
			age,
			desc,
		)
		fmt.Fprintln(os.Stdout, line)
	}

	fmt.Fprintf(os.Stdout, "\n  %d %s\n", total, plural(total, "skill", "skills"))
	return nil
}

// formatAge returns a human-readable age string for the given time.
func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dw ago", int(d.Hours()/(24*7)))
	}
}

// truncate shortens s to at most max runes, appending "..." if truncated.
func truncate(s string, max int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max-3]) + "..."
}

// plural returns singular when n == 1, otherwise plural.
func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}

func runListInstalled(cfg *config.Config) error {
	dir := config.DefaultDir()
	fullCfg, _, err := config.EnsureSkillsKey(dir)
	if err != nil {
		ui.Errorf("load config: %s", err)
		return nil
	}

	state, _ := config.ReadState(dir)
	stateMap := make(map[string]config.SyncedSkill, len(state.Skills))
	for _, s := range state.Skills {
		stateMap[s.Name] = s
	}

	if ui.JSONMode {
		type installedSkill struct {
			Name    string `json:"name"`
			Scope   string `json:"scope"`
			Version int    `json:"version,omitempty"`
		}
		var out []installedSkill
		for _, s := range fullCfg.Skills {
			scope := s.Scope
			if scope == "" {
				scope = fullCfg.Scope
				if scope == "" {
					scope = "project"
				}
			}
			ver := 0
			if synced, ok := stateMap[s.Name]; ok {
				ver = synced.Version
			}
			out = append(out, installedSkill{Name: s.Name, Scope: scope, Version: ver})
		}
		return ui.PrintJSON(map[string]interface{}{"skills": out, "total": len(out)})
	}

	if len(fullCfg.Skills) == 0 {
		fmt.Fprintln(os.Stdout, "  No skills installed.")
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "  Try: skael add <skill-name>")
		return nil
	}

	for _, s := range fullCfg.Skills {
		scope := s.Scope
		if scope == "" {
			scope = fullCfg.Scope
			if scope == "" {
				scope = "project"
			}
		}
		ver := "not synced"
		if synced, ok := stateMap[s.Name]; ok {
			ver = fmt.Sprintf("v%d", synced.Version)
		}
		fmt.Fprintf(os.Stdout, "  %-24s %-8s  %s\n", s.Name, ver, scope)
	}
	fmt.Fprintf(os.Stdout, "\n  %d %s\n", len(fullCfg.Skills), plural(len(fullCfg.Skills), "skill", "skills"))
	return nil
}
