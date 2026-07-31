package scan

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/skael-dev/skael/internal/gate"
	"golang.org/x/text/unicode/norm"
)

// maxScanBytes caps how much of a single file is scanned. Files larger than this
// are scanned up to the cap and flagged as truncated, rather than skipped — a
// silent skip would let an attacker hide a payload by padding past the limit.
const maxScanBytes = 1 << 20 // 1 MiB

// allRules is the combined set of all detection rules, populated at init time
// from AllRules() so the two definitions cannot drift.
var allRules []Rule

func init() {
	allRules = AllRules()
}

// ScanDir walks a directory tree, scans each file, and returns an aggregated report.
// Binary files and files larger than maxScanBytes are not fully scanned but are
// surfaced as informational findings rather than skipped silently.
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

		truncated := info.Size() > maxScanBytes
		data, err := readCapped(path, maxScanBytes)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		// A binary file inside a skill is unusual and can't be meaningfully
		// regex-scanned; surface it as informational rather than skipping silently
		// (a NUL byte must not be a way to smuggle an unscanned payload).
		if isBinary(data) {
			addFileFinding(report, "UNSCANNED_FILE", path,
				"Binary file not scanned (skills should contain text)")
			return nil
		}

		scanContent(path, string(data), report)

		// Flag oversized files so reviewers know only the first maxScanBytes were
		// examined.
		if truncated {
			addFileFinding(report, "TRUNCATED_FILE", path,
				fmt.Sprintf("File exceeds %d bytes; only the first %d were scanned", info.Size(), maxScanBytes))
		}
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
		// Skip placeholders/references a rule explicitly excludes.
		if rule.Reject != nil && rule.Reject.MatchString(match) {
			continue
		}
		shown := maskMatch(match)
		if rule.Category == "secrets" {
			// Never echo a credential verbatim in the report.
			shown = maskSecret(match)
		}
		class, _ := gate.ClassOf(rule.Category)
		report.Findings = append(report.Findings, Finding{
			Rule:       rule.Name,
			Severity:   rule.Severity,
			Confidence: rule.Confidence,
			File:       filename,
			Line:       lineNum,
			Match:      shown,
			Message:    rule.Message,
			Class:      string(class),
		})
	}
}

// scanContent runs all rules against the content line-by-line and appends findings.
// It also scans consecutive line pairs to catch secrets split across two lines.
// After both passes it deduplicates findings by rule+file+line so that a secret
// that exists entirely on one line is not reported twice.
func scanContent(filename, content string, report *Report) {
	// Drop a single leading BOM so a normal UTF-8 file isn't flagged for it.
	content = strings.TrimPrefix(content, "\uFEFF")

	lines := strings.Split(content, "\n")
	for lineNum, line := range lines {
		scanVariants(filename, line, lineNum+1, report)
	}
	// Also scan consecutive line pairs to catch secrets split across two lines.
	for i := 0; i < len(lines)-1; i++ {
		combined := lines[i] + lines[i+1]
		scanVariants(filename, combined, i+1, report)
	}

	// Structural shell-AST analysis (Phase 2): catches dangerous shell
	// constructs the line-based regexes miss (split pipelines, eval of dynamic
	// content, etc.). Runs on shell scripts and fenced shell blocks in markdown.
	scanShell(filename, content, report)

	// Deduplicate: keep only the first finding for each rule+file+line combination.
	report.Findings = dedupeFindings(report.Findings)
}

// dedupeFindings keeps only the first finding for each rule+file+line key.
func dedupeFindings(findings []Finding) []Finding {
	seen := map[string]bool{}
	deduped := make([]Finding, 0, len(findings))
	for _, f := range findings {
		key := fmt.Sprintf("%s:%s:%d", f.Rule, f.File, f.Line)
		if !seen[key] {
			seen[key] = true
			deduped = append(deduped, f)
		}
	}
	return deduped
}

// Finalize dedupes the report's findings and recomputes its summary and status.
// Call it after merging in findings from an external scanner so the publish
// block-on-status logic sees the combined result.
func Finalize(report *Report) {
	report.Findings = dedupeFindings(report.Findings)
	report.Summary = computeSummary(report)
	report.Status = computeStatus(report)
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

// scanVariants scans a line as-is and, when normalization changes it (unicode
// tricks present), scans the normalized form too. The raw pass catches the
// hidden-character obfuscation rules; the normalized pass defeats evasion that
// uses zero-width/compatibility characters to break up an otherwise-flagged
// phrase. Dedup in scanContent collapses any overlap.
func scanVariants(filename, line string, lineNum int, report *Report) {
	scanLine(filename, line, lineNum, report)
	if n := normalizeForScan(line); n != line {
		scanLine(filename, n, lineNum, report)
	}
}

// invisibleStripper removes zero-width and bidirectional control characters so
// that the normalized pass sees the underlying text an attacker tried to hide.
var invisibleStripper = strings.NewReplacer(
	"\u200B", "", "\u200C", "", "\u200D", "", "\u2060", "", "\uFEFF", "", "\u00AD", "",
	"\u202A", "", "\u202B", "", "\u202C", "", "\u202D", "", "\u202E", "",
	"\u2066", "", "\u2067", "", "\u2068", "", "\u2069", "",
)

// normalizeForScan applies Unicode NFKC normalization (folding compatibility
// variants such as full-width characters) and strips invisible formatting
// characters, yielding the text to match rules against for evasion resistance.
func normalizeForScan(s string) string {
	return invisibleStripper.Replace(norm.NFKC.String(s))
}

// maskSecret reduces a matched credential to a short identifying prefix so the
// scan report never echoes the full secret value.
func maskSecret(match string) string {
	const keep = 6
	r := []rune(match)
	if len(r) <= keep {
		return "****"
	}
	return string(r[:keep]) + "…REDACTED"
}

// readCapped reads up to max bytes from the file at path.
func readCapped(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max))
	if err != nil {
		return nil, err
	}
	return data, nil
}

// addFileFinding appends a file-level (non-line) informational finding.
func addFileFinding(report *Report, rule, file, message string) {
	report.Findings = append(report.Findings, Finding{
		Rule:       rule,
		Severity:   "info",
		Confidence: "high",
		File:       file,
		Line:       0,
		Message:    message,
	})
}
