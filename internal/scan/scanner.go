package scan

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// allRules is the combined set of all detection rules, populated at init time.
var allRules []Rule

func init() {
	allRules = append(allRules, secretRules...)
	allRules = append(allRules, injectionRules...)
	allRules = append(allRules, exfiltrationRules...)
	allRules = append(allRules, obfuscationRules...)
}

// ScanDir walks a directory tree, scans each file, and returns an aggregated report.
// Binary files and files larger than 1 MiB are skipped.
func ScanDir(dir string) (*Report, error) {
	report := &Report{
		Findings: []Finding{},
	}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		// Skip files larger than 1 MiB
		if info.Size() > 1<<20 {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		// Skip binary files (heuristic: NUL byte present)
		if isBinary(data) {
			return nil
		}

		scanContent(path, string(data), report)
		return nil
	})
	if err != nil {
		return nil, err
	}

	report.Summary = computeSummary(report)
	report.Status = computeStatus(report)
	return report, nil
}

// ScanContent scans a single file's content and returns a completed report.
func ScanContent(filename, content string) *Report {
	report := &Report{
		Findings: []Finding{},
	}
	scanContent(filename, content, report)
	report.Summary = computeSummary(report)
	report.Status = computeStatus(report)
	return report
}

// scanLine checks a single line of text against all rules and appends findings.
func scanLine(filename, line string, lineNum int, report *Report) {
	for _, rule := range allRules {
		match := rule.Pattern.FindString(line)
		if match == "" {
			continue
		}
		report.Findings = append(report.Findings, Finding{
			Rule:       rule.Name,
			Severity:   rule.Severity,
			Confidence: rule.Confidence,
			File:       filename,
			Line:       lineNum,
			Match:      maskMatch(match),
			Message:    rule.Message,
		})
	}
}

// scanContent runs all rules against the content line-by-line and appends findings.
// It also scans consecutive line pairs to catch secrets split across two lines.
// After both passes it deduplicates findings by rule+file+line so that a secret
// that exists entirely on one line is not reported twice.
func scanContent(filename, content string, report *Report) {
	lines := strings.Split(content, "\n")
	for lineNum, line := range lines {
		scanLine(filename, line, lineNum+1, report)
	}
	// Also scan consecutive line pairs to catch secrets split across two lines.
	for i := 0; i < len(lines)-1; i++ {
		combined := lines[i] + lines[i+1]
		scanLine(filename, combined, i+1, report)
	}

	// Deduplicate: keep only the first finding for each rule+file+line combination.
	seen := map[string]bool{}
	deduped := []Finding{}
	for _, f := range report.Findings {
		key := fmt.Sprintf("%s:%s:%d", f.Rule, f.File, f.Line)
		if !seen[key] {
			seen[key] = true
			deduped = append(deduped, f)
		}
	}
	report.Findings = deduped
}

// maskMatch truncates long matches to avoid leaking sensitive values in reports.
// Matches longer than 40 chars become: first 20 chars + "****" + last 8 chars.
func maskMatch(match string) string {
	if len(match) <= 40 {
		return match
	}
	return match[:20] + "****" + match[len(match)-8:]
}

// computeStatus determines the overall report status based on the most severe finding.
func computeStatus(r *Report) string {
	for _, f := range r.Findings {
		if f.Severity == "critical" {
			return "critical"
		}
	}
	for _, f := range r.Findings {
		if f.Severity == "high" {
			return "warn"
		}
	}
	for _, f := range r.Findings {
		if f.Severity == "medium" || f.Severity == "info" {
			return "info"
		}
	}
	return "clean"
}

// computeSummary counts findings by severity.
func computeSummary(r *Report) Summary {
	var s Summary
	for _, f := range r.Findings {
		switch f.Severity {
		case "critical":
			s.Critical++
		case "high":
			s.High++
		case "medium":
			s.Medium++
		case "info":
			s.Info++
		}
	}
	return s
}

// isBinary returns true if data contains a NUL byte, indicating a binary file.
func isBinary(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}
