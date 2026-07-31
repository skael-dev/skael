package lint

import (
	"fmt"
	"path/filepath"

	"github.com/skael-dev/skael/internal/scan"
)

// Injection runs the platform's security scanner over the bundle and maps its
// findings onto lint findings.
//
// The ruleset is deliberately not duplicated here. internal/scan already
// handles unicode normalization, zero-width stripping, line-pair matching, and
// structural shell parsing; a second pattern set in the engine would be
// strictly worse and would diverge on every scanner fix.
func Injection(bundleDir string) ([]Finding, error) {
	report, err := scan.ScanDir(bundleDir)
	if err != nil {
		return nil, fmt.Errorf("lint.Injection: %w", err)
	}

	out := make([]Finding, 0, len(report.Findings))
	for _, f := range report.Findings {
		rel, relErr := filepath.Rel(bundleDir, f.File)
		if relErr != nil {
			rel = f.File
		}
		// The scanner walks the whole directory, so it also reads the eval
		// sidecar. Nothing under the sidecar is ever packed, and its oracle
		// and verifier scripts are written to drive a task — an oracle that
		// provisions its own sandbox is indistinguishable, to a scanner
		// looking at shipped skill content, from an attack.
		if slashRel := filepath.ToSlash(rel); Excluded(slashRel) {
			continue
		}
		out = append(out, Finding{
			Rule:     "scan/" + f.Rule,
			Severity: severityFor(f.Severity),
			File:     filepath.ToSlash(rel),
			Line:     f.Line,
			// The scanner already masks secret values into f.Message (and
			// truncates f.Match); carrying Message through unchanged rather
			// than re-deriving a message from the file preserves that
			// masking. Re-reading the file here would undo it and reprint a
			// credential in the finding.
			Message: f.Message,
		})
	}
	return out, nil
}

// severityFor maps scanner severities onto lint severities. critical and high
// become errors: a bundle carrying an exfiltration or credential pattern must
// not lint clean.
func severityFor(s string) Severity {
	switch s {
	case "critical", "high":
		return SeverityError
	case "medium":
		return SeverityWarn
	default:
		return SeverityInfo
	}
}
