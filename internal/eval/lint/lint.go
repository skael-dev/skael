// Package lint validates a skill bundle across three layers: spec conformance,
// quality, and injection (security scan).
package lint

// Severity classifies how serious a finding is.
type Severity string

const (
	// SeverityError findings fail a bundle: ExitCode returns 1 if any exist.
	SeverityError Severity = "error"
	// SeverityWarn findings are advisory and do not affect ExitCode.
	SeverityWarn Severity = "warn"
	// SeverityInfo findings are informational only.
	SeverityInfo Severity = "info"
)

// Finding is a single lint result: a rule violated at a location in the bundle.
type Finding struct {
	Rule     string
	Severity Severity
	File     string
	Line     int
	Message  string
}

// Result aggregates every finding from every lint layer for a bundle.
type Result struct {
	Findings []Finding
}

// Errors returns the number of SeverityError findings.
func (r *Result) Errors() int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			n++
		}
	}
	return n
}

// Warnings returns the number of SeverityWarn findings.
func (r *Result) Warnings() int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == SeverityWarn {
			n++
		}
	}
	return n
}

// HasErrors reports whether any finding is SeverityError.
func (r *Result) HasErrors() bool {
	return r.Errors() > 0
}

// ExitCode maps a result onto a process exit code. Warnings do not fail.
func (r *Result) ExitCode() int {
	if r.HasErrors() {
		return 1
	}
	return 0
}

// Run executes every lint layer against a bundle directory.
func Run(bundleDir string) (*Result, error) {
	res := &Result{}
	for _, layer := range []func(string) ([]Finding, error){Conformance, Quality, Injection} {
		found, err := layer(bundleDir)
		if err != nil {
			return nil, err
		}
		res.Findings = append(res.Findings, found...)
	}
	return res, nil
}
