package cli

import (
	"fmt"
	"strconv"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var reviewCmd = &cobra.Command{
	Use:   "review <skill-name> <version>",
	Short: "Approve or reject a version held by the publish gate",
	Args:  cobra.ExactArgs(2),
	RunE:  runReview,
}

var (
	reviewApprove bool
	reviewReject  bool
	reviewReason  string
)

func init() {
	reviewCmd.Flags().BoolVar(&reviewApprove, "approve", false, "Approve the held version")
	reviewCmd.Flags().BoolVar(&reviewReject, "reject", false, "Reject the held version")
	reviewCmd.Flags().StringVar(&reviewReason, "reason", "", "Written justification, recorded on the version (required)")
	rootCmd.AddCommand(reviewCmd)
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

	ver, err := c.Review(name, version, action, reviewReason)
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

	if action == "approve" {
		ui.Success("%s v%d approved — now served to clients", name, ver.Version)
	} else {
		ui.Success("%s v%d rejected", name, ver.Version)
	}
	return nil
}
