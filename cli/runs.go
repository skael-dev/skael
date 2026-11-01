package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Report the activations of automated runs",
}

var eventsReportCmd = &cobra.Command{
	Use:   "report --file <path>",
	Short: "Send a batch of run events (service account key)",
	Long: `Send a batch of run events. The batch is all or nothing.

The file is JSON Lines with one event on each line, a JSON array of events, or
an object with an "events" array. Each event has skill, version, agent, role,
external_run_id and time.`,
	Args: cobra.NoArgs,
	RunE: runEventsReport,
}

var outcomesCmd = &cobra.Command{
	Use:   "outcomes",
	Short: "Report and read how automated runs ended",
}

var outcomesReportCmd = &cobra.Command{
	Use:   "report --run <id> --outcome <outcome>",
	Short: "Report how a run ended (service account key)",
	Long: `Report how a run ended. A second report for one run replaces the first.

An outcome never changes a headline and never releases a version.`,
	Args: cobra.NoArgs,
	RunE: runOutcomesReport,
}

var outcomesShowCmd = &cobra.Command{
	Use:   "show <skill>",
	Short: "Show the outcomes of the runs that loaded each version of a skill",
	Args:  cobra.ExactArgs(1),
	RunE:  runOutcomesShow,
}

var (
	eventsFile           string
	eventsIdempotencyKey string
	outcomeReport        client.OutcomeReport
)

func init() {
	eventsReportCmd.Flags().StringVar(&eventsFile, "file", "", "A JSON or JSON Lines file of events")
	eventsReportCmd.Flags().StringVar(&eventsIdempotencyKey, "idempotency-key", "", "A repeat with this key returns the first result and stores nothing")
	_ = eventsReportCmd.MarkFlagRequired("file")
	eventsCmd.AddCommand(eventsReportCmd)
	rootCmd.AddCommand(eventsCmd)

	flags := outcomesReportCmd.Flags()
	flags.StringVar(&outcomeReport.ExternalRunID, "run", "", "Your id for the run")
	flags.StringVar(&outcomeReport.Outcome, "outcome", "", "succeeded, failed or cancelled")
	flags.IntVar(&outcomeReport.Repairs, "repairs", 0, "How many repairs the run needed")
	flags.StringVar(&outcomeReport.ReviewVerdict, "verdict", "", "The verdict of the review of the run")
	flags.StringVar(&outcomeReport.EvidenceURL, "evidence", "", "An http or https link to the evidence")
	flags.StringVar(&outcomeReport.Set, "set", "", "The set the run used")
	flags.StringVar(&outcomeReport.ResolutionID, "resolution", "", "The resolution the run used")
	_ = outcomesReportCmd.MarkFlagRequired("run")
	_ = outcomesReportCmd.MarkFlagRequired("outcome")
	outcomesCmd.AddCommand(outcomesReportCmd, outcomesShowCmd)
	rootCmd.AddCommand(outcomesCmd)
}

// parseEvents reads the three file shapes. An object with an events key is a
// batch, and any other object is one event, so JSON Lines needs no special case.
func parseEvents(data []byte) ([]json.RawMessage, error) {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '[' {
		var events []json.RawMessage
		err := json.Unmarshal(data, &events)
		return events, err
	}
	var events []json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var value json.RawMessage
		err := dec.Decode(&value)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("event %d: %w", len(events), err)
		}
		events = append(events, value)
	}
	if len(events) == 1 {
		var batch struct {
			Events []json.RawMessage `json:"events"`
		}
		if json.Unmarshal(events[0], &batch) == nil && batch.Events != nil {
			return batch.Events, nil
		}
	}
	return events, nil
}

func runEventsReport(cmd *cobra.Command, args []string) error {
	data, err := os.ReadFile(eventsFile)
	if err != nil {
		return usageError("invalid_file", "cannot read events file: "+err.Error(), "")
	}
	events, err := parseEvents(data)
	if err != nil {
		return usageError("invalid_file", fmt.Sprintf("%s is not valid JSON: %s", eventsFile, err), "write JSON Lines, a JSON array or an object with an events array")
	}
	if len(events) == 0 {
		return usageError("invalid_file", eventsFile+" holds no events", "")
	}
	c, err := loadClient()
	if err != nil {
		return err
	}
	res, err := c.ReportEvents(events, eventsIdempotencyKey)
	if err != nil {
		if index, detail, ok := client.BatchFailure(err); ok {
			return &exitError{
				exit: exitAPI, code: "invalid_event", msg: detail,
				context: fmt.Sprintf("failing index: %d. The server stored nothing from this batch.", index),
				extra:   map[string]any{"index": index},
			}
		}
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	ui.Success("Stored %d %s in %d %s", res.Value.Stored, plural(res.Value.Stored, "event", "events"), res.Value.Runs, plural(res.Value.Runs, "run", "runs"))
	return nil
}

func runOutcomesReport(cmd *cobra.Command, args []string) error {
	if !slices.Contains([]string{"succeeded", "failed", "cancelled"}, outcomeReport.Outcome) {
		return usageError("invalid_outcome", fmt.Sprintf("invalid outcome %q", outcomeReport.Outcome), "use succeeded, failed or cancelled")
	}
	if outcomeReport.Repairs < 0 {
		return usageError("invalid_repairs", "--repairs cannot be negative", "")
	}
	c, err := loadClient()
	if err != nil {
		return err
	}
	res, err := c.ReportOutcome(outcomeReport)
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	ui.Success("Recorded %s for run %s as %s", outcomeReport.Outcome, res.Value.ExternalRunID, res.Value.Consumer)
	return nil
}

func runOutcomesShow(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	res, err := c.GetSkillOutcomes(args[0])
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	if len(res.Value.Consumers) == 0 {
		fmt.Fprintf(os.Stdout, "  No run has loaded %s.\n", args[0])
		return nil
	}
	for _, consumer := range res.Value.Consumers {
		fmt.Fprintf(os.Stdout, "  %s\n", ui.Bold(consumer.Consumer))
		for _, v := range consumer.Versions {
			fmt.Fprintf(os.Stdout, "    v%-4d %s\n", v.Version, outcomeLine(v, res.Value.MinComparableRuns))
		}
	}
	return nil
}

// outcomeLine states counts and differences only. It never claims a cause.
func outcomeLine(v client.VersionOutcomes, minRuns int) string {
	line := fmt.Sprintf("%d %s: %d succeeded, %d failed, %d cancelled, %d no outcome",
		v.Runs, plural(v.Runs, "run", "runs"), v.Succeeded, v.Failed, v.Cancelled, v.NoOutcome)
	if v.SampleTooSmall || v.SuccessRate == nil || v.MeanRepairs == nil {
		return line + fmt.Sprintf(". The sample is too small: rates need %d runs", minRuns)
	}
	line += fmt.Sprintf(". Success %.1f%%, mean repairs %.2f", *v.SuccessRate*100, *v.MeanRepairs)
	if v.Comparison != nil {
		line += fmt.Sprintf(". Against v%d: success %+.1f points, repairs %+.2f",
			v.Comparison.PreviousVersion, v.Comparison.SuccessRateChange*100, v.Comparison.MeanRepairsChange)
	}
	return line
}
