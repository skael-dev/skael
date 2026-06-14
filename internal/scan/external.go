package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// ExternalScanner is an opt-in, operator-configured external security scanner
// (Phase 2, SDD external.go). skael shells out to a free/OSS tool — e.g.
// gitleaks, Cisco skill-scanner, or Semgrep with operator-authored rules — runs
// it over the already-unpacked skill directory, and merges its SARIF output into
// the native report. Nothing is bundled: the operator installs the tool, so
// skael's licensing stays clean (subprocess only, no linking).
//
// Findings are namespaced (e.g. "gitleaks:generic-api-key") and merged via
// Finalize so the same critical/warn → block-on-publish logic applies. If the
// tool is missing or errors, the caller logs and continues on the native
// scanner alone — an optional external must never hard-block a publish.
type ExternalScanner struct {
	// Name namespaces the rule IDs in merged findings.
	Name string
	// Command is the argv to execute; the token "{dir}" is replaced with the
	// skill directory. The command must emit SARIF JSON on stdout.
	Command []string
	// Timeout bounds a single scan; defaults to 60s when zero.
	Timeout time.Duration
}

// NewExternalScanner builds a scanner from a whitespace-separated command line,
// or returns nil when cmdline is empty (feature disabled). The first token is
// used as the scanner Name when it has no explicit name.
func NewExternalScanner(cmdline string, timeout time.Duration) *ExternalScanner {
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return nil
	}
	return &ExternalScanner{Name: fields[0], Command: fields, Timeout: timeout}
}

// Run executes the external scanner over dir and returns the parsed findings.
func (e *ExternalScanner) Run(ctx context.Context, dir string) ([]Finding, error) {
	if e == nil || len(e.Command) == 0 {
		return nil, nil
	}
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := substituteDir(e.Command, dir)
	cmd := exec.CommandContext(cctx, args[0], args[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	if cctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("external scanner %q timed out after %s", e.Name, timeout)
	}

	// Parse stdout regardless of exit code: scanners like gitleaks exit non-zero
	// precisely when they find issues.
	findings, perr := parseSARIF(stdout.Bytes(), e.Name)
	if perr != nil {
		if runErr != nil {
			// Couldn't run and couldn't parse — surface the run failure.
			return nil, fmt.Errorf("external scanner %q failed: %w: %s", e.Name, runErr, strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("external scanner %q: parse SARIF output: %w", e.Name, perr)
	}
	if runErr != nil && len(findings) == 0 && stdout.Len() == 0 {
		// Ran, produced nothing, and failed (e.g. binary missing / bad args).
		return nil, fmt.Errorf("external scanner %q failed: %w: %s", e.Name, runErr, strings.TrimSpace(stderr.String()))
	}
	return findings, nil
}

// MergeExternal runs the (optional) external scanner over dir and merges its
// findings into report, recomputing status. It is best-effort: if the scanner
// is unset it is a no-op, and if it fails it logs a warning and leaves the
// native report untouched — an optional external tool must never block a publish
// because it is missing or misconfigured.
func MergeExternal(ctx context.Context, ext *ExternalScanner, dir string, report *Report) {
	if ext == nil {
		return
	}
	findings, err := ext.Run(ctx, dir)
	if err != nil {
		log.Warn().Err(err).Str("scanner", ext.Name).
			Msg("external scanner failed; continuing with native scan only")
		return
	}
	if len(findings) == 0 {
		return
	}
	report.Findings = append(report.Findings, findings...)
	Finalize(report)
}

// substituteDir returns a copy of cmd with every "{dir}" token replaced by dir.
func substituteDir(cmd []string, dir string) []string {
	out := make([]string, len(cmd))
	for i, a := range cmd {
		out[i] = strings.ReplaceAll(a, "{dir}", dir)
	}
	return out
}

// --- SARIF (minimal subset) ---

type sarifLog struct {
	Runs []struct {
		Tool struct {
			Driver struct {
				Name string `json:"name"`
			} `json:"driver"`
		} `json:"tool"`
		Results []struct {
			RuleID  string `json:"ruleId"`
			Level   string `json:"level"`
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
			Locations []struct {
				PhysicalLocation struct {
					ArtifactLocation struct {
						URI string `json:"uri"`
					} `json:"artifactLocation"`
					Region struct {
						StartLine int `json:"startLine"`
					} `json:"region"`
				} `json:"physicalLocation"`
			} `json:"locations"`
		} `json:"results"`
	} `json:"runs"`
}

// parseSARIF maps a SARIF document into skael findings. Empty input means no
// findings (not an error). Rule IDs are namespaced with the scanner name.
func parseSARIF(data []byte, name string) ([]Finding, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}
	var log sarifLog
	if err := json.Unmarshal(data, &log); err != nil {
		return nil, err
	}
	var findings []Finding
	for _, run := range log.Runs {
		for _, r := range run.Results {
			rule := r.RuleID
			if rule == "" {
				rule = "finding"
			}
			file, line := "", 0
			if len(r.Locations) > 0 {
				pl := r.Locations[0].PhysicalLocation
				file = pl.ArtifactLocation.URI
				line = pl.Region.StartLine
			}
			findings = append(findings, Finding{
				Rule:       name + ":" + rule,
				Severity:   sarifLevelToSeverity(r.Level),
				Confidence: "medium",
				File:       file,
				Line:       line,
				Message:    strings.TrimSpace(r.Message.Text),
			})
		}
	}
	return findings, nil
}

// sarifLevelToSeverity maps a SARIF level to skael severity. "error" maps to
// "high" (which blocks publishing); softer levels are advisory.
func sarifLevelToSeverity(level string) string {
	switch strings.ToLower(level) {
	case "error":
		return "high"
	case "warning":
		return "medium"
	default: // note, none, "" or unknown
		return "info"
	}
}
