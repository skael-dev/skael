package lint

import (
	"fmt"
	"path/filepath"

	"github.com/skael-dev/skael/internal/scan"
)

// Injection runs the security scanner over the bundle and maps its findings
// onto lint findings.
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
		// Skip the eval sidecar — nothing under it is packed.
		if slashRel := filepath.ToSlash(rel); Excluded(slashRel) {
			continue
		}
		out = append(out, Finding{
			Rule:     "scan/" + f.Rule,
			Severity: severityFor(f.Severity),
			File:     filepath.ToSlash(rel),
			Line:     f.Line,
			// Carry the scanner's message unchanged to preserve its secret masking.
			Message: f.Message,
		})
	}
	return out, nil
}

// severityFor maps scanner severities onto lint severities.
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
