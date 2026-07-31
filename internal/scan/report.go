package scan

import (
	"path/filepath"
	"strings"
)

// Relativize rewrites every finding's File to a path relative to root.
//
// A server-side scan runs against a throwaway unpack directory, so the raw
// paths are meaningless to the publisher and disclose the server's filesystem
// layout to anyone who can publish. Findings are also persisted in
// scan_result and rendered by the CLI and the review UI, so the rewrite has
// to happen once, at the scan site, before anything reads them.
//
// A path that is not under root is left alone: it is not the scanner's job to
// invent a relationship that isn't there.
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

	// Class groups the finding by whether an empirical measurement could
	// overturn it. Derived from the matched rule's Category via
	// gate.ClassOf. Empty on findings deserialized from a scan_result
	// written before this field existed.
	//
	// "class" is a persisted wire name, not just a response field: it is
	// stored in the scan_result JSONB column. Renaming it would make
	// Reconsider read an empty class off every existing row, hit Decide's
	// fail-closed default, and hold those versions permanently.
	Class string `json:"class,omitempty"`
}

// Summary aggregates finding counts by severity.
type Summary struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Info     int `json:"info"`
}
