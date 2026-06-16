package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:   "show <skill>",
	Short: "Show details of a skill",
	Args:  cobra.ExactArgs(1),
	RunE:  runShow,
}

var showVersions bool

func init() {
	showCmd.Flags().BoolVar(&showVersions, "versions", false, "List all versions")
	rootCmd.AddCommand(showCmd)
}

func runShow(cmd *cobra.Command, args []string) error {
	name := args[0]

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

	sk, err := c.GetSkill(name)
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "api_error", "")
			return nil
		}
		ui.Errorf("%s", err)
		return nil
	}
	if sk == nil {
		if ui.JSONMode {
			ui.PrintJSONError("skill not found: "+name, "not_found", "skael list")
			return nil
		}
		ui.Error(ui.ErrorDetail{
			Message:    "skill not found: " + name,
			Suggestion: "skael list",
		})
		return nil
	}

	var versions []client.Version
	if showVersions || ui.JSONMode {
		versions, err = c.ListVersions(name)
		if err != nil {
			if ui.JSONMode {
				ui.PrintJSONError(err.Error(), "api_error", "")
				return nil
			}
			ui.Errorf("%s", err)
			return nil
		}
	}

	activations, err := c.GetActivations(name)
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "api_error", "")
			return nil
		}
		ui.Errorf("%s", err)
		return nil
	}

	if ui.JSONMode {
		out := struct {
			Skill       *client.Skill          `json:"skill"`
			Versions    []client.Version       `json:"versions,omitempty"`
			Activations *client.ActivationSummary `json:"activations"`
		}{
			Skill:       sk,
			Versions:    versions,
			Activations: activations,
		}
		return ui.PrintJSON(out)
	}

	fm := parseFrontmatter(sk.Frontmatter)

	fmt.Fprintf(os.Stdout, "\n  %s v%d\n", ui.Bold(sk.Name), sk.LatestVersion)
	fmt.Fprintln(os.Stdout, "")

	metaParts := []string{}
	if author := fmString(fm, "author"); author != "" {
		metaParts = append(metaParts, "By: "+author)
	}
	if license := fmString(fm, "license"); license != "" {
		metaParts = append(metaParts, "License: "+license)
	}
	hasSpec := fmString(fm, "spec") != ""
	if hasSpec {
		metaParts = append(metaParts, "Spec: ✓")
	} else {
		metaParts = append(metaParts, "Spec: ·")
	}
	if len(metaParts) > 0 {
		fmt.Fprintf(os.Stdout, "    %s\n", strings.Join(metaParts, "  |  "))
	}

	if sk.Description != "" {
		fmt.Fprintf(os.Stdout, "    %s\n", sk.Description)
	}

	tags := fmTags(fm)
	if len(tags) > 0 {
		fmt.Fprintf(os.Stdout, "    Tags: %s\n", strings.Join(tags, ", "))
	}

	fmt.Fprintf(os.Stdout, "    Last published: %s\n", formatAge(sk.UpdatedAt))
	fmt.Fprintf(os.Stdout, "    Versions: %d\n", sk.LatestVersion)

	if activations.TotalCount > 0 {
		fmt.Fprintf(os.Stdout, "    Activations (30d): %d", activations.TotalCount)
		if activations.UniqueDevs > 0 {
			fmt.Fprintf(os.Stdout, "  ·  %d %s", activations.UniqueDevs, plural(activations.UniqueDevs, "developer", "developers"))
		}
		fmt.Fprintln(os.Stdout)

		if len(activations.ByAgent) > 0 {
			agents := make([]string, 0, len(activations.ByAgent))
			for a := range activations.ByAgent {
				agents = append(agents, a)
			}
			sort.Strings(agents)
			parts := make([]string, 0, len(agents))
			for _, a := range agents {
				parts = append(parts, fmt.Sprintf("%s: %d", a, activations.ByAgent[a]))
			}
			fmt.Fprintf(os.Stdout, "    By agent: %s\n", strings.Join(parts, "  ·  "))
		}
	}

	if showVersions && len(versions) > 0 {
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "  Versions:")
		for i := len(versions) - 1; i >= 0; i-- {
			v := versions[i]
			age := formatAge(v.CreatedAt)
			if v.Changelog != "" {
				fmt.Fprintf(os.Stdout, "    v%d  %-10s  %s\n", v.Version, age, v.Changelog)
			} else {
				fmt.Fprintf(os.Stdout, "    v%d  %s\n", v.Version, age)
			}
		}
	}

	fmt.Fprintln(os.Stdout, "")
	return nil
}

// parseFrontmatter decodes a JSON frontmatter blob into a map. Returns nil on error.
func parseFrontmatter(raw json.RawMessage) map[string]interface{} {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// fmString returns the string value of key in the frontmatter map, or "".
func fmString(fm map[string]interface{}, key string) string {
	if fm == nil {
		return ""
	}
	v, ok := fm[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// fmTags returns the []string value of the "tags" key, or nil.
func fmTags(fm map[string]interface{}) []string {
	if fm == nil {
		return nil
	}
	v, ok := fm["tags"]
	if !ok {
		return nil
	}
	raw, ok := v.([]interface{})
	if !ok {
		return nil
	}
	tags := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			tags = append(tags, s)
		}
	}
	return tags
}
