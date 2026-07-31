package whetstone

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/ui"
)

var reportOpen bool

var reportCmd = &cobra.Command{
	Use:   "report <skill> [ref]",
	Short: "Render the HTML report for one eval",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		skill := args[0]
		ref := "latest"
		if len(args) > 1 {
			ref = args[1]
		}
		path, err := RunReport(skill, ref, reportOpen, nil)
		if err != nil {
			return err
		}
		if ui.JSONMode {
			return ui.PrintJSON(map[string]string{"skill": skill, "path": path})
		}
		ui.Success("report for %s written to %s", skill, path)
		return nil
	},
}

// RunReport renders the HTML report for skill's ref ("latest" or an eval id)
// to <sidecar>/reports/<eval id>/report.html and returns that path. When
// open is true it hands the path to opener; opener defaults to defaultOpener
// (nil is not a caller error — the command above always passes it explicitly,
// but a test that only cares about the render path can pass nil too as long
// as open is false).
//
// opener is injected rather than this function shelling out to a browser
// itself: a test that launches a browser is a test nobody runs twice.
func RunReport(skill, ref string, open bool, opener func(path string) error) (string, error) {
	st, err := openStore()
	if err != nil {
		return "", err
	}
	defer func() { _ = st.Close() }()

	doc, evalID, err := resolveReportDoc(st, skill, ref)
	if err != nil {
		return "", err
	}
	rep, err := report.Load(bytes.NewReader(doc))
	if err != nil {
		return "", fmt.Errorf("whetstone report: eval %d: %w", evalID, err)
	}

	evalDir, err := st.EvalDir(skill)
	if err != nil {
		return "", err
	}
	reportsDir := filepath.Join(evalDir, "reports", strconv.FormatInt(evalID, 10))
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		return "", fmt.Errorf("whetstone report: creating report directory: %w", err)
	}
	htmlPath := filepath.Join(reportsDir, "report.html")
	if err := writeReportFile(htmlPath, rep.HTML); err != nil {
		return "", err
	}

	if open {
		op := opener
		if op == nil {
			op = defaultOpener
		}
		if err := op(htmlPath); err != nil {
			// Opening is a courtesy on top of a file that already exists;
			// the caller still gets a usable path back rather than a failed
			// command.
			ui.Warn("could not open %s: %v", htmlPath, err)
		}
	}

	return htmlPath, nil
}

// defaultOpener resolves the platform's "open a file with the default
// application" command: `open` on darwin, `xdg-open` elsewhere. When neither
// exists on PATH it reports the path rather than failing — the user can open
// it themselves.
func defaultOpener(path string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	bin, err := exec.LookPath(name)
	if err != nil {
		ui.Info("no %s found on PATH; open it yourself: %s", name, path)
		return nil
	}
	return exec.Command(bin, path).Start()
}

func init() {
	reportCmd.Flags().BoolVar(&reportOpen, "open", false, "Open the rendered report with the OS default handler")
	rootCmd.AddCommand(reportCmd)
}
