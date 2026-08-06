package cli

import (
	"fmt"
	"os"
	"strconv"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var reviewCmd = &cobra.Command{
	Use:   "review <skill-name> <version>",
	Short: "Approve or reject a version held by the publish gate",
	Args:  cobra.ExactArgs(2),
	RunE:  runReview,
}

var reviewShowCmd = &cobra.Command{
	Use:   "show <skill-name> [version]",
	Short: "Show the diff and outstanding review reasons for a held version",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runReviewShow,
}

var (
	reviewApprove    bool
	reviewReject     bool
	reviewReason     string
	reviewReasonKind string
)

func init() {
	reviewCmd.Flags().BoolVar(&reviewApprove, "approve", false, "Approve the held version")
	reviewCmd.Flags().BoolVar(&reviewReject, "reject", false, "Reject the held version")
	reviewCmd.Flags().StringVar(&reviewReason, "reason", "", "Written justification, recorded on the version (required)")
	// --reason-kind is deliberately distinct from --reason: --reason is the
	// written justification, required and already validated server-side.
	// --reason-kind is which held reason (scan or ownership) this decision
	// targets. Overloading --reason for both would break every deployed
	// `skael review --approve --reason "..."` invocation.
	reviewCmd.Flags().StringVar(&reviewReasonKind, "reason-kind", "",
		"Which held reason this decision targets (scan or ownership); required when more than one is outstanding")
	reviewCmd.AddCommand(reviewShowCmd)
	rootCmd.AddCommand(reviewCmd)
}

// whoClears names, in one short phrase, who is allowed to clear a hold
// reason of this kind. It mirrors the authorization split the server
// enforces in registerReviewRoutes: a scan finding is instance-level only,
// an ownership hold can also be cleared by the skill's own owner.
func whoClears(reasonKind string) string {
	switch reasonKind {
	case gate.ReasonScan:
		return "an owner or admin"
	case gate.ReasonOwnership:
		return "an owner of this skill, or an admin"
	default:
		return "an owner or admin"
	}
}

func runReview(cmd *cobra.Command, args []string) error {
	name := args[0]
	version, err := strconv.Atoi(args[1])
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError("version must be an integer", "invalid_version", "")
			return nil
		}
		ui.Errorf("version must be an integer: %s", args[1])
		return nil
	}

	// Validate the flag combination locally before any request — a round
	// trip to learn --approve and --reject conflict, or that --reason is
	// missing, is a wasted call and a worse experience than failing fast.
	if reviewApprove == reviewReject {
		msg := "exactly one of --approve or --reject is required"
		if ui.JSONMode {
			ui.PrintJSONError(msg, "invalid_flags", "")
			return nil
		}
		ui.Errorf("%s", msg)
		return fmt.Errorf("%s", msg)
	}
	if reviewReason == "" {
		msg := "--reason is required"
		if ui.JSONMode {
			ui.PrintJSONError(msg, "invalid_flags", "")
			return nil
		}
		ui.Errorf("%s", msg)
		return fmt.Errorf("%s", msg)
	}
	// --reason-kind, when given, must name a hold reason that actually
	// exists. The server would 422 on anything else, but there is no reason
	// to spend a round trip finding that out — this is exactly the same
	// "validate what we can locally first" discipline as the two checks
	// above.
	if reviewReasonKind != "" && reviewReasonKind != gate.ReasonScan && reviewReasonKind != gate.ReasonOwnership {
		msg := fmt.Sprintf("--reason-kind must be %q or %q, got %q",
			gate.ReasonScan, gate.ReasonOwnership, reviewReasonKind)
		if ui.JSONMode {
			ui.PrintJSONError(msg, "invalid_flags", "")
			return nil
		}
		ui.Errorf("%s", msg)
		return fmt.Errorf("%s", msg)
	}

	action := "approve"
	if reviewReject {
		action = "reject"
	}

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

	ver, err := c.Review(name, version, action, reviewReason, reviewReasonKind)
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "api_error", "")
			return nil
		}
		ui.Errorf("%s", err)
		return nil
	}

	if ui.JSONMode {
		return ui.PrintJSON(ver)
	}

	switch {
	case action == "reject":
		// Rejecting any single reason rejects the whole version — there is
		// no partial-reject state — so this is unconditionally terminal.
		ui.Success("%s v%d rejected", name, ver.Version)
	case ver.GateState == "released":
		ui.Success("%s v%d approved — now served to clients", name, ver.Version)
	default:
		// GateState is still needs_review: another reason is still
		// outstanding. This can only happen when reviewReasonKind was given
		// explicitly — an omitted hold_reason only ever resolves the sole
		// outstanding reason, which always fully releases — so
		// reviewReasonKind names exactly what this call just cleared, and
		// the rest of ver.HoldReasons (the full set this version was ever
		// held for) minus that one is what remains.
		//
		// This branch must never say "released": that is the specific way a
		// two-reason hold would otherwise lie to the caller.
		var remaining []string
		for _, r := range ver.HoldReasons {
			if r != reviewReasonKind {
				remaining = append(remaining, r)
			}
		}
		ui.Warn("%s v%d: %s cleared, but still held for review", name, ver.Version, reviewReasonKind)
		for _, r := range remaining {
			ui.Summary("Outstanding", fmt.Sprintf("%s — clears via %s", r, whoClears(r)))
		}
	}
	return nil
}

func runReviewShow(cmd *cobra.Command, args []string) error {
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

	// The review queue is where "outstanding" (as opposed to "ever held
	// for") reasons live — GET .../review endpoint's Version response
	// doesn't carry that distinction. It also lets `review show name` (no
	// version) find whichever version of name is actually held, without a
	// second round trip once the version is known.
	held, err := c.ReviewQueue()
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "api_error", "")
			return nil
		}
		ui.Errorf("%s", err)
		return nil
	}

	var entry *client.HeldVersion
	version := 0
	if len(args) == 2 {
		version, err = strconv.Atoi(args[1])
		if err != nil {
			if ui.JSONMode {
				ui.PrintJSONError("version must be an integer", "invalid_version", "")
				return nil
			}
			ui.Errorf("version must be an integer: %s", args[1])
			return nil
		}
		for i := range held {
			if held[i].SkillName == name && held[i].Version == version {
				entry = &held[i]
				break
			}
		}
	} else {
		for i := range held {
			if held[i].SkillName == name && held[i].Version > version {
				version = held[i].Version
				entry = &held[i]
			}
		}
		if version == 0 {
			msg := fmt.Sprintf("no version of %s is held for review", name)
			if ui.JSONMode {
				ui.PrintJSONError(msg, "not_held", "")
				return nil
			}
			ui.Errorf("%s", msg)
			return nil
		}
	}

	diff, err := c.DiffVersion(name, version)
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
			Diff        *client.VersionDiff `json:"diff"`
			HoldReasons []string            `json:"hold_reasons,omitempty"`
			Outstanding []string            `json:"outstanding,omitempty"`
		}{Diff: diff}
		if entry != nil {
			out.HoldReasons = entry.HoldReasons
			out.Outstanding = entry.Outstanding
		}
		return ui.PrintJSON(out)
	}

	fmt.Fprintf(os.Stdout, "  %s v%d\n\n", name, version)
	if diff.SkillMD != "" {
		fmt.Fprintln(os.Stdout, diff.SkillMD)
	} else {
		fmt.Fprintln(os.Stdout, "  SKILL.md unchanged")
	}
	for _, f := range diff.Files {
		fmt.Fprintf(os.Stdout, "  %-10s %s\n", f.Status, f.Path)
	}

	if entry != nil && len(entry.Outstanding) > 0 {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "  Outstanding:")
		for _, r := range entry.Outstanding {
			fmt.Fprintf(os.Stdout, "    %s — clears via %s\n", r, whoClears(r))
		}
	}
	return nil
}
