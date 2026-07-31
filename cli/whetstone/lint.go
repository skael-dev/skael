package whetstone

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/lint"
	"github.com/skael-dev/skael/internal/ui"
)

var lintStrict bool

var lintCmd = &cobra.Command{
	Use:   "lint <skill|path>",
	Short: "Run every lint layer over a skill bundle",
	Long: "Run spec conformance, quality, and injection lint over a bundle.\n\n" +
		"The argument is either a bundle directory or the name of a skill in the\n" +
		"workspace. The exit code is the CI signal: 0 unless there are errors,\n" +
		"with --strict promoting warnings to errors.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveBundle(args[0])
		if err != nil {
			return err
		}
		code, err := RunLint(dir, lintStrict)
		if err != nil {
			return err
		}
		if code != 0 {
			os.Exit(code)
		}
		return nil
	},
}

// lintOutput is the --json shape. It carries the exit code explicitly so a
// caller reading the JSON does not have to re-derive the strict-mode rule.
type lintOutput struct {
	Bundle   string        `json:"bundle"`
	Strict   bool          `json:"strict"`
	Errors   int           `json:"errors"`
	Warnings int           `json:"warnings"`
	ExitCode int           `json:"exit_code"`
	Findings []lintFinding `json:"findings"`
}

type lintFinding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
}

// RunLint lints the bundle at path and returns the process exit code.
//
// The error return is reserved for lint being unable to run at all (an
// unreadable bundle); findings are reported through the exit code, so a
// failing bundle is not also an error the caller has to special-case.
func RunLint(path string, strict bool) (int, error) {
	res, code, err := lintBundle(path, strict)
	if err != nil {
		return 0, err
	}

	if ui.JSONMode {
		out := lintOutput{
			Bundle:   path,
			Errors:   res.Errors(),
			Warnings: res.Warnings(),
			ExitCode: code,
			Strict:   strict,
			Findings: make([]lintFinding, 0, len(res.Findings)),
		}
		for _, f := range res.Findings {
			out.Findings = append(out.Findings, lintFinding{
				Rule:     f.Rule,
				Severity: string(f.Severity),
				File:     f.File,
				Line:     f.Line,
				Message:  f.Message,
			})
		}
		return code, ui.PrintJSON(out)
	}

	renderFindings(res)

	switch {
	case len(res.Findings) == 0:
		ui.Success("%s lints clean", path)
	case code != 0 && res.Errors() == 0:
		ui.Summary(plural(res.Warnings(), "warning"), "failing under --strict")
	default:
		ui.Summary(plural(res.Errors(), "error"), plural(res.Warnings(), "warning"))
	}

	return code, nil
}

// lintBundle runs every lint layer and returns the result alongside the exit
// code the CLI would report for it. It is what `gen` and `new` use: those
// commands own the single JSON document on stdout, so they need the outcome
// without RunLint's rendering.
func lintBundle(path string, strict bool) (*lint.Result, int, error) {
	dir, err := bundleRoot(path)
	if err != nil {
		return nil, 0, err
	}

	res, err := lint.Run(dir)
	if err != nil {
		return nil, 0, err
	}

	code := res.ExitCode()
	if strict && res.Warnings() > 0 {
		code = 1
	}
	return res, code, nil
}

// bundleRoot resolves a bundle path to an absolute directory.
//
// lint derives the skill's expected name from the last element of the
// directory it is handed, so a relative "." — packing or linting the bundle
// you are standing in, the most natural invocation — would be compared against
// the frontmatter name as the literal string ".", failing every clean bundle
// with a name-dir-mismatch. Resolving first is what makes that path work. The
// caller keeps the path the user typed for display.
func bundleRoot(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("whetstone: resolving %q: %w", path, err)
	}
	return abs, nil
}

// renderFindings prints one line per finding, at the severity's own style. It
// is a no-op under --json, where findings travel inside the JSON document.
func renderFindings(res *lint.Result) {
	for _, f := range res.Findings {
		line := fmt.Sprintf("%s %s: %s", location(f), f.Rule, f.Message)
		switch f.Severity {
		case lint.SeverityError:
			ui.Errorf("%s", line)
		case lint.SeverityWarn:
			ui.Warn("%s", line)
		default:
			ui.Info("%s", line)
		}
	}
}

// location renders a finding's file and line as "file:line".
func location(f lint.Finding) string {
	if f.Line > 0 {
		return fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	return f.File
}

// plural renders a count with its noun, so a summary never reads "1 warnings".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// resolveBundle accepts either a directory on disk or the name of a skill in
// the workspace. A path is tried first: a directory that exists is what the
// author meant, and requiring a workspace to lint a bundle that is sitting
// right there would make the CI use of lint useless.
func resolveBundle(arg string) (string, error) {
	if info, err := os.Stat(arg); err == nil && info.IsDir() {
		return arg, nil
	}

	st, err := openStore()
	if err != nil {
		return "", fmt.Errorf("%q is not a directory, and %w", arg, err)
	}
	defer func() { _ = st.Close() }()

	dir, err := st.SkillDir(arg)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("no bundle at %q and no skill named %q in the workspace", arg, arg)
	}
	return dir, nil
}

func init() {
	lintCmd.Flags().BoolVar(&lintStrict, "strict", false, "Treat warnings as errors")
	rootCmd.AddCommand(lintCmd)
}
