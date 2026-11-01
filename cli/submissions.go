package cli

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var submissionsCmd = &cobra.Command{
	Use:   "submissions",
	Short: "Send a skill for human review and read the decision",
	Long: `Send a skill for human review and read the decision.

No command here publishes or decides. A person decides a submission in the web
UI, and an approved submission then passes through the normal publish gate.`,
}

var submissionsSubmitCmd = &cobra.Command{
	Use:   "submit <dir> --name <name> --rationale <text>",
	Short: "Send a skill directory for human review",
	Args:  cobra.ExactArgs(1),
	RunE:  runSubmissionsSubmit,
}

var submissionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List submissions (a person's key)",
	Args:  cobra.NoArgs,
	RunE:  runSubmissionsList,
}

var submissionsGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Show the state of a submission and the reason for its decision",
	Args:  cobra.ExactArgs(1),
	RunE:  runSubmissionsGet,
}

var (
	submissionRequest client.SubmissionRequest
	submissionsState  string
)

func init() {
	flags := submissionsSubmitCmd.Flags()
	flags.StringVar(&submissionRequest.Name, "name", "", "The skill name")
	flags.StringVar(&submissionRequest.Rationale, "rationale", "", "Why this skill or this change is needed")
	flags.StringVar(&submissionRequest.RunLink, "run-link", "", "An http or https link to the run that produced it")
	flags.StringVar(&submissionRequest.ExternalRunID, "run-id", "", "Your id for the run that produced it")
	_ = submissionsSubmitCmd.MarkFlagRequired("name")
	_ = submissionsSubmitCmd.MarkFlagRequired("rationale")
	submissionsListCmd.Flags().StringVar(&submissionsState, "state", "", "pending, approved or rejected")
	submissionsCmd.AddCommand(submissionsSubmitCmd, submissionsListCmd, submissionsGetCmd)
	rootCmd.AddCommand(submissionsCmd)
}

func runSubmissionsSubmit(cmd *cobra.Command, args []string) error {
	req := submissionRequest
	if !skillNameRe.MatchString(req.Name) {
		return usageError("invalid_name", fmt.Sprintf("invalid skill name %q", req.Name), "use lowercase letters, digits, colons, dots and hyphens")
	}
	if req.Rationale == "" {
		return usageError("invalid_rationale", "--rationale cannot be empty", "")
	}
	if _, err := os.Stat(filepath.Join(args[0], "SKILL.md")); err != nil {
		return usageError("missing_skill_md", "SKILL.md not found in "+args[0], "")
	}
	archive, _, _, err := skill.Pack(args[0])
	if err != nil {
		return fmt.Errorf("pack %s: %w", args[0], err)
	}
	req.ArchiveBase64 = base64.StdEncoding.EncodeToString(archive)
	c, err := loadClient()
	if err != nil {
		return err
	}
	res, decision, err := c.CreateSubmission(req)
	if err != nil {
		if decision != nil && decision.Outcome == gate.Block {
			if !ui.JSONMode {
				printUnappealable(*decision)
			}
			return &exitError{exit: exitAPI, code: "submission_blocked", msg: err.Error(), extra: map[string]any{"decision": decision}}
		}
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	ui.Success("Submitted %s for review", res.Value.SkillName)
	fmt.Fprintf(os.Stdout, "  id: %s\n  A person decides this in the web UI. Read the decision with: skael submissions get %s\n", res.Value.ID, res.Value.ID)
	return nil
}

func runSubmissionsList(cmd *cobra.Command, args []string) error {
	if submissionsState != "" && !slices.Contains([]string{"pending", "approved", "rejected"}, submissionsState) {
		return usageError("invalid_state", fmt.Sprintf("invalid state %q", submissionsState), "use pending, approved or rejected")
	}
	c, err := loadClient()
	if err != nil {
		return err
	}
	res, err := c.ListSubmissions(submissionsState)
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	if len(res.Value.Submissions) == 0 {
		fmt.Fprintln(os.Stdout, "  No submissions.")
		return nil
	}
	for _, s := range res.Value.Submissions {
		fmt.Fprintf(os.Stdout, "  %s  %-24s %-9s %-20s %s\n", s.ID, s.SkillName, s.State, truncate(s.Consumer, 20), formatAge(s.CreatedAt))
	}
	return nil
}

func runSubmissionsGet(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	res, err := c.GetSubmission(args[0])
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	s := res.Value
	fmt.Fprintf(os.Stdout, "  %s  %s\n  state: %s\n", ui.Bold(s.SkillName), s.ID, s.State)
	if s.Reason != "" {
		fmt.Fprintf(os.Stdout, "  reason: %s\n", s.Reason)
	}
	if s.DecidedBy != "" {
		fmt.Fprintf(os.Stdout, "  decided by: %s\n", s.DecidedBy)
	}
	if s.Version != nil {
		fmt.Fprintf(os.Stdout, "  version: %d\n", *s.Version)
	}
	return nil
}
