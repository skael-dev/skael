// Package lint validates a generated or imported skill bundle. Three layers,
// all deterministic: spec conformance (does this satisfy the Agent Skills
// format), quality (is it written the way skills that actually work are
// written), and injection (does it carry a security risk).
//
// Conformance delegates to internal/skill's validator rather than restating its
// rules, so a bundle cannot pass lint here and fail compliance at publish.
// Injection delegates to internal/scan rather than defining a second pattern
// set, for the same reason.
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

// ExitCode maps a result onto a process exit code. Warnings do not fail: a
// pre-commit hook that fails on advisory findings is a hook that gets removed.
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

// Quality and Injection are defined in quality.go and injection.go
// respectively.
