package scan

import (
	"path/filepath"
	"strings"
)

// Relativize rewrites every finding's File to a path relative to root, so a
// server-side scan does not leak the host's filesystem layout.
func Relativize(report *Report, root string) {
	if report == nil {
		return
	}
	for i := range report.Findings {
		rel, err := filepath.Rel(root, report.Findings[i].File)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		report.Findings[i].File = filepath.ToSlash(rel)
	}
}

// Report is the result of scanning a skill archive or content for security issues.
type Report struct {
	Status   string    `json:"status"` // clean, info, warn, critical
	Findings []Finding `json:"findings"`
	Summary  Summary   `json:"summary"`
}

// Finding describes a single matched security rule.
type Finding struct {
	Rule       string `json:"rule"`
	Severity   string `json:"severity"`   // critical, high, medium, info
	Confidence string `json:"confidence"` // high, medium, low
	File       string `json:"file"`
	Line       int    `json:"line"`
	Match      string `json:"match"`
	Message    string `json:"message"`

	// Persisted wire name in scan_result JSONB; do not rename.
	Class string `json:"class,omitempty"`
}

// Summary aggregates finding counts by severity.
type Summary struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Info     int `json:"info"`
}
