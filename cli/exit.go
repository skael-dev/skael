package cli

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

// The exit codes are a public contract: scripts branch on them.
const (
	exitAPI            = 1
	exitUsage          = 2
	exitIncompleteSet  = 10
	exitDigestMismatch = 11
)

const setupSuggestion = "skael setup <url> <api-key>"

// exitError carries a process exit code and the error envelope for a failure.
// run is the only place that prints it.
type exitError struct {
	exit       int
	code       string
	msg        string
	context    string
	suggestion string
	// extra joins the JSON envelope, for failures with structure to report.
	extra map[string]any
	// rendered means the command already printed its full result.
	rendered bool
}

func (e *exitError) Error() string {
	if e.msg == "" {
		return fmt.Sprintf("exit %d", e.exit)
	}
	return e.msg
}

func usageError(code, msg, suggestion string) *exitError {
	return &exitError{exit: exitUsage, code: code, msg: msg, suggestion: suggestion}
}

func failure(code, msg, suggestion string) *exitError {
	return &exitError{exit: exitAPI, code: code, msg: msg, suggestion: suggestion}
}

func notConfigured(err error) *exitError {
	return &exitError{exit: exitUsage, code: "not_configured", msg: "not configured", context: err.Error(), suggestion: setupSuggestion}
}

func apiFailure(err error) *exitError { return failure("api_error", err.Error(), "") }

// renderedExit sets the exit code of a command whose output already says what
// happened, such as a scan report with findings.
func renderedExit(exit int) *exitError { return &exitError{exit: exit, rendered: true} }

var prepareOnce sync.Once

// prepareCommands marks every error a command's RunE returns, so an unmarked
// error reaching run can only be cobra's own parsing error: a usage error.
func prepareCommands(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		prepareCommands(sub)
	}
	if cmd.HasParent() && !cmd.Runnable() {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageError("usage_error", fmt.Sprintf("unknown command %q for %q", args[0], c.CommandPath()), c.CommandPath()+" --help")
			}
			return c.Help()
		}
		return
	}
	if runE := cmd.RunE; runE != nil {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			err := runE(c, args)
			var marked *exitError
			if err == nil || errors.As(err, &marked) {
				return err
			}
			return failure("error", err.Error(), "")
		}
	}
}

// run executes one command line and returns the process exit code.
func run(args []string) int {
	prepareOnce.Do(func() { prepareCommands(rootCmd) })
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	if err == nil {
		return 0
	}
	// Cobra fails on an unknown command or flag before it parses --json.
	if slices.Contains(args, "--json") {
		ui.JSONMode = true
	}
	var ee *exitError
	if !errors.As(err, &ee) {
		ee = usageError("usage_error", err.Error(), "skael --help")
	}
	report(ee)
	return ee.exit
}

func report(e *exitError) {
	if e.rendered {
		return
	}
	if !ui.JSONMode {
		ui.Error(ui.ErrorDetail{Message: e.msg, Context: e.context, Suggestion: e.suggestion})
		return
	}
	envelope := map[string]any{"error": e.msg, "code": e.code}
	if e.suggestion != "" {
		envelope["suggestion"] = e.suggestion
	}
	for k, v := range e.extra {
		envelope[k] = v
	}
	if err := ui.PrintJSON(envelope); err != nil {
		ui.PrintJSONError(e.msg, e.code, e.suggestion)
	}
}
