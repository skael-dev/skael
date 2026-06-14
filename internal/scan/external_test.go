package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const sampleSARIF = `{
  "version": "2.1.0",
  "runs": [
    {
      "tool": { "driver": { "name": "gitleaks" } },
      "results": [
        {
          "ruleId": "generic-api-key",
          "level": "error",
          "message": { "text": "API key detected" },
          "locations": [
            { "physicalLocation": {
                "artifactLocation": { "uri": "config.yaml" },
                "region": { "startLine": 7 }
            } }
          ]
        },
        {
          "ruleId": "todo-note",
          "level": "note",
          "message": { "text": "informational" },
          "locations": [
            { "physicalLocation": {
                "artifactLocation": { "uri": "README.md" },
                "region": { "startLine": 1 }
            } }
          ]
        }
      ]
    }
  ]
}`

func TestParseSARIF_MapsResults(t *testing.T) {
	findings, err := parseSARIF([]byte(sampleSARIF), "gitleaks")
	if err != nil {
		t.Fatalf("parseSARIF error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d: %+v", len(findings), findings)
	}
	got := findings[0]
	if got.Rule != "gitleaks:generic-api-key" {
		t.Errorf("rule not namespaced: %q", got.Rule)
	}
	if got.Severity != "high" { // SARIF error -> high (blocking)
		t.Errorf("expected severity high for SARIF error, got %q", got.Severity)
	}
	if got.File != "config.yaml" || got.Line != 7 {
		t.Errorf("expected config.yaml:7, got %s:%d", got.File, got.Line)
	}
	if findings[1].Severity != "info" { // SARIF note -> info (non-blocking)
		t.Errorf("expected severity info for SARIF note, got %q", findings[1].Severity)
	}
}

func TestParseSARIF_EmptyIsNoFindings(t *testing.T) {
	for _, in := range []string{"", "   \n", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"x"}},"results":[]}]}`} {
		findings, err := parseSARIF([]byte(in), "x")
		if err != nil {
			t.Fatalf("parseSARIF(%q) error: %v", in, err)
		}
		if len(findings) != 0 {
			t.Errorf("expected 0 findings for %q, got %d", in, len(findings))
		}
	}
}

func TestSubstituteDir(t *testing.T) {
	got := substituteDir([]string{"gitleaks", "dir", "{dir}", "--sarif"}, "/tmp/skill")
	if got[2] != "/tmp/skill" {
		t.Errorf("expected {dir} substituted, got %q", got[2])
	}
	// must not mutate the input slice
	if got[0] != "gitleaks" {
		t.Errorf("unexpected arg0 %q", got[0])
	}
}

func TestExternalScanner_Run_ParsesStdout(t *testing.T) {
	dir := t.TempDir()
	sarifPath := filepath.Join(dir, "out.sarif")
	if err := os.WriteFile(sarifPath, []byte(sampleSARIF), 0644); err != nil {
		t.Fatalf("write sarif: %v", err)
	}
	es := &ExternalScanner{Name: "gitleaks", Command: []string{"cat", sarifPath}, Timeout: 10 * time.Second}
	findings, err := es.Run(context.Background(), dir)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d: %+v", len(findings), findings)
	}
}

func TestExternalScanner_Run_NonZeroExitStillParses(t *testing.T) {
	// Tools like gitleaks exit non-zero when they FIND issues; we must still
	// parse their SARIF stdout rather than treat the exit code as failure.
	dir := t.TempDir()
	sarifPath := filepath.Join(dir, "out.sarif")
	if err := os.WriteFile(sarifPath, []byte(sampleSARIF), 0644); err != nil {
		t.Fatalf("write sarif: %v", err)
	}
	es := &ExternalScanner{Name: "gitleaks", Command: []string{"sh", "-c", "cat " + sarifPath + "; exit 1"}, Timeout: 10 * time.Second}
	findings, err := es.Run(context.Background(), dir)
	if err != nil {
		t.Fatalf("Run error (non-zero exit should not fail): %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
}

func TestExternalScanner_Run_MissingBinaryErrors(t *testing.T) {
	es := &ExternalScanner{Name: "nope", Command: []string{"this-binary-does-not-exist-skael", "{dir}"}, Timeout: 5 * time.Second}
	_, err := es.Run(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected an error when the external binary is missing")
	}
}

// TestMergeExternal_BlocksOnExternalFinding mirrors the publish path: a clean
// native scan plus a high-severity external finding must end up blocking
// (status "warn"). A nil scanner must be a no-op.
func TestMergeExternal_BlocksOnExternalFinding(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# clean skill\n"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	// Keep the SARIF outside the scanned dir so it isn't itself scanned.
	sarifPath := filepath.Join(t.TempDir(), "ext.sarif")
	if err := os.WriteFile(sarifPath, []byte(sampleSARIF), 0644); err != nil {
		t.Fatalf("write sarif: %v", err)
	}

	report, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}

	// nil scanner: no change.
	MergeExternal(context.Background(), nil, dir, report)
	if report.Status != "clean" {
		t.Fatalf("nil scanner should be a no-op; got status %q", report.Status)
	}

	// configured scanner emitting a high (SARIF error) finding: now blocks.
	es := &ExternalScanner{Name: "gitleaks", Command: []string{"cat", sarifPath}, Timeout: 10 * time.Second}
	MergeExternal(context.Background(), es, dir, report)
	if report.Status != "warn" {
		t.Errorf("expected status warn after external high finding, got %q", report.Status)
	}
	if findingWithRule(report.Findings, "gitleaks:generic-api-key") == nil {
		t.Errorf("expected merged external finding, got: %+v", report.Findings)
	}
}

func TestFinalize_MergesExternalFindings(t *testing.T) {
	// A clean native report plus a high-severity external finding must become
	// status "warn" (blocking) after merge + finalize.
	report := ScanContent("SKILL.md", "# clean\n")
	if report.Status != "clean" {
		t.Fatalf("precondition: expected clean, got %q", report.Status)
	}
	report.Findings = append(report.Findings, Finding{
		Rule: "gitleaks:generic-api-key", Severity: "high", Confidence: "medium",
		File: "config.yaml", Line: 7, Message: "API key detected",
	})
	Finalize(report)
	if report.Status != "warn" {
		t.Errorf("expected status warn after merging a high finding, got %q", report.Status)
	}
	if report.Summary.High != 1 {
		t.Errorf("expected Summary.High==1, got %d", report.Summary.High)
	}
}
