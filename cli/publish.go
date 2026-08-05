package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/gate"
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

var (
	publishSkipLocalScan bool
	publishOverride      bool
	publishForce         bool // deprecated alias for --skip-local-scan
)

func init() {
	publishCmd.Flags().BoolVar(&publishSkipLocalScan, "skip-local-scan", false,
		"Skip the local security scan and let the server decide")
	publishCmd.Flags().BoolVar(&publishOverride, "override", false,
		"Publish despite blocking findings (owner/admin only, recorded server-side)")
	publishCmd.Flags().BoolVar(&publishForce, "force", false,
		"Deprecated alias for --skip-local-scan")
	_ = publishCmd.Flags().MarkDeprecated("force",
		"use --skip-local-scan to skip the local scan, or --override to publish despite findings")
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
	sp := StartSpinner("Scanning...")
	report, err := scan.ScanDir(dir)
	if err != nil {
		sp.Stop()
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "scan_error", "")
			return nil
		}
		ui.Errorf("%s", err)
		return nil
	}
	sp.Stop()

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

	// The local scan exists to save an upload that the server would refuse.
	// That is now a narrower set than "any blocking finding": an appealable
	// finding is no longer a rejection, it is the review path. So decide
	// locally with the *same* function the server uses — not a reimplemented
	// threshold, which would drift — and abort only on a Block. Anything the
	// server would hold gets sent, held, and reported as held.
	//
	// --skip-local-scan skips this check entirely; the server scans again and
	// can still reject. --override is what gets a privileged user past a
	// finding that would otherwise hold the version.
	skipLocalScan := publishSkipLocalScan || publishForce
	localDecision := gate.Decide(*report, nil, gate.OwnerState{}, gate.Policy{})

	if localDecision.Outcome == gate.Block && !skipLocalScan && !publishOverride {
		if ui.JSONMode {
			return encodeDecision(localDecision)
		}
		printUnappealable(localDecision)
		ui.Error(ui.ErrorDetail{
			Message: "publish blocked: bundle contains unappealable findings",
		})
		return nil
	}

	if localDecision.Held() && !skipLocalScan && !publishOverride && !ui.JSONMode {
		fmt.Fprintln(os.Stdout,
			"  These findings do not block publishing, but they will hold the version for review.")
	}

	const blockedSuggestion = "fix the findings above, or ask an owner or admin to publish with --override"

	// Pack the skill directory into a tar.gz archive
	sp = StartSpinner("Packing archive...")
	archive, _, entries, err := skill.Pack(dir)
	if err != nil {
		sp.Stop()
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "pack_error", "")
			return nil
		}
		ui.Errorf("%s", err)
		return nil
	}
	sp.Stop()

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
	sp = StartSpinner("Uploading...")
	existing, err := c.GetSkill(name)
	if err != nil {
		sp.Stop()
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
			sp.Stop()
			if ui.JSONMode {
				ui.PrintJSONError(err.Error(), "api_error", "")
				return nil
			}
			ui.Errorf("%s", err)
			return nil
		}
	}

	// Publish the new version
	ver, serverReport, decision, pubErr := c.PublishVersion(name, archive, publishOverride)
	sp.Stop()
	if pubErr != nil {
		if apiErr, ok := pubErr.(*client.APIError); ok && apiErr.StatusCode == http.StatusUnprocessableEntity {
			// A Block outcome is unappealable: no evaluation, no admin
			// override, clears it. Suggesting --override here would send the
			// operator after a permission that cannot help.
			if decision != nil && decision.Outcome == gate.Block {
				if ui.JSONMode {
					return encodeDecision(*decision)
				}
				printUnappealable(*decision)
				ui.Error(ui.ErrorDetail{
					Message: "publish blocked: archive contains unappealable findings",
				})
				return nil
			}

			if ui.JSONMode {
				ui.PrintJSONError("publish blocked by server-side security scan", "scan_blocked", blockedSuggestion)
				return nil
			}
			// Show what the server actually objected to. Repeating the local
			// scan's verdict here would be guesswork — the server may run an
			// external scanner the client does not have.
			if serverReport != nil && len(serverReport.Findings) > 0 {
				fmt.Fprintln(os.Stdout, "\n  Server scan findings:")
				for _, f := range serverReport.Findings {
					fmt.Fprintf(os.Stdout, "  %s:%d\t%-10s  %s\n",
						f.File, f.Line, f.Severity, f.Message)
				}
			} else {
				fmt.Fprintf(os.Stdout, "\n  %s\n", apiErr.Message)
			}
			ui.Error(ui.ErrorDetail{
				Message:    "publish blocked by server-side security scan",
				Suggestion: blockedSuggestion,
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
			Name      string        `json:"name"`
			Version   int           `json:"version"`
			Created   bool          `json:"created"`
			Decision  gate.Decision `json:"decision"`
			GateState string        `json:"gate_state"`
		}{
			Name:      name,
			Version:   ver.Version,
			Created:   ver.Created,
			Decision:  ver.Decision,
			GateState: ver.GateState,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if !ver.Created {
		// An unchanged-checksum republish returns the version's *persisted*
		// gate state, not a fresh recompute — GateState is read back from
		// the row, so it cannot be stale the way a recomputed Decision could
		// be (e.g. a version held on first publish, then approved by an
		// admin: GateState is "released" even though re-deciding from
		// scratch with no quality evidence would say needs_review again).
		// Branch on GateState, not on Decision.Held().
		switch ver.GateState {
		case "needs_review":
			ui.Info("No changes detected — v%d is unchanged (still held for review)", ver.Version)
			return nil
		case "rejected":
			ui.Info("No changes detected — v%d is unchanged (rejected, not served)", ver.Version)
			return nil
		}
		ui.Info("No changes detected — v%d is already up to date", ver.Version)
		return nil
	}

	switch ver.Decision.Outcome {
	case gate.NeedsReview:
		fmt.Fprintf(os.Stdout, "  ⏸ %s v%d created and held for review\n", name, ver.Version)
		fmt.Fprintln(os.Stdout, "  It is not served to any client until it is cleared.")
		fmt.Fprintln(os.Stdout)
		for _, r := range ver.Decision.Reasons {
			fmt.Fprintf(os.Stdout, "  %s:%d  %s (%s, %s)\n", r.File, r.Line, r.Rule, r.Class, r.Severity)
			fmt.Fprintf(os.Stdout, "    %s\n", r.Message)
			fmt.Fprintf(os.Stdout, "    Clears: %s\n", r.Clears)
		}
		fmt.Fprintln(os.Stdout)
		if ver.Quality.State == "pending" {
			fmt.Fprintln(os.Stdout, "  An evaluation has been queued. To approve it by hand instead:")
		} else {
			fmt.Fprintln(os.Stdout, "  No evaluation suite is registered for this skill, so nothing will")
			fmt.Fprintln(os.Stdout, "  clear it automatically. An owner or admin can approve it:")
		}
		fmt.Fprintf(os.Stdout, "    skael review %s %d --approve --reason \"...\"\n", name, ver.Version)
	case gate.AllowWithWarning:
		fmt.Fprintf(os.Stdout, "  ⚠ %s v%d published with warnings\n", name, ver.Version)
		for _, r := range ver.Decision.Reasons {
			fmt.Fprintf(os.Stdout, "  %s:%d  %s  %s\n", r.File, r.Line, r.Rule, r.Message)
		}
		fmt.Fprintf(os.Stdout, "  %s/skills/%s\n", cfg.Endpoint, name)
	default:
		fmt.Fprintf(os.Stdout, "  ✓ Published v%d\n", ver.Version)
		fmt.Fprintf(os.Stdout, "  %s/skills/%s\n", cfg.Endpoint, name)
	}
	return nil
}

// printUnappealable renders a Block decision. Local and server-side blocks
// share it so a publisher sees the same thing wherever the block was decided —
// the two are the same verdict from the same function, and presenting them
// differently would suggest they are appealable in different ways.
func printUnappealable(d gate.Decision) {
	fmt.Fprintln(os.Stdout, "\n  Blocked — unappealable findings:")
	for _, r := range d.Reasons {
		fmt.Fprintf(os.Stdout, "  %s:%d\t%s (%s, %s)\n    %s\n    Clears: %s\n",
			r.File, r.Line, r.Rule, r.Class, r.Severity, r.Message, r.Clears)
	}
}

// encodeDecision writes a decision as the sole key of a JSON object, matching
// the shape the server returns on a 422 so a CI job can read
// .decision.outcome without caring which side decided.
func encodeDecision(d gate.Decision) error {
	out := struct {
		Decision gate.Decision `json:"decision"`
	}{Decision: d}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
