package ui

import (
	"fmt"
	"os"
	"strings"
)

// JSONMode when true, all styled output functions are no-ops (stdout remains clean for JSON).
var JSONMode bool

// ErrorDetail holds structured error information for display.
type ErrorDetail struct {
	Message    string
	Context    string
	Suggestion string
}

// Success prints a success message to stderr: "  ✓ message"
func Success(format string, args ...interface{}) {
	if JSONMode {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, styleSuccess.Render("  ✓ "+msg))
}

// Warn prints a warning message to stderr: "  ! message"
func Warn(format string, args ...interface{}) {
	if JSONMode {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, styleWarn.Render("  ! "+msg))
}

// Download prints a download line to stderr: "  ↓ name                    version"
func Download(name, version string) {
	if JSONMode {
		return
	}
	line := fmt.Sprintf("  ↓ %-24s%s", name, version)
	fmt.Fprintln(os.Stderr, styleAccent.Render(line))
}

// New prints a new-install line to stderr: "  + name                    version (new)"
func New(name, version string) {
	if JSONMode {
		return
	}
	line := fmt.Sprintf("  + %-24s%s (new)", name, version)
	fmt.Fprintln(os.Stderr, styleSuccess.Render(line))
}

// Summary prints a summary line to stderr: "\n  part1 · part2 · part3"
func Summary(parts ...string) {
	if JSONMode {
		return
	}
	joined := strings.Join(parts, styleMuted.Render(" · "))
	fmt.Fprintln(os.Stderr, "\n  "+joined)
}

// Error prints a structured error to stderr.
// Format:
//
//	  ✗ message
//
//	    context
//
//	    Try: suggestion
func Error(detail ErrorDetail) {
	if JSONMode {
		return
	}
	fmt.Fprintln(os.Stderr, styleError.Render("  ✗ "+detail.Message))
	if detail.Context != "" {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, styleMuted.Render("    "+detail.Context))
	}
	if detail.Suggestion != "" {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, styleDim.Render("    Try: "+detail.Suggestion))
	}
}

// Errorf prints a simple formatted error to stderr: "  ✗ message"
func Errorf(format string, args ...interface{}) {
	if JSONMode {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, styleError.Render("  ✗ "+msg))
}

// Info prints an informational message to stderr: "  · message"
func Info(format string, args ...interface{}) {
	if JSONMode {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, styleMuted.Render("  · "+msg))
}

// Bold returns a bold-styled string.
func Bold(s string) string {
	return styleBold.Render(s)
}

// Code returns a code-styled string.
func Code(s string) string {
	return styleCode.Render(s)
}

// Accent returns an accent-styled string.
func Accent(s string) string {
	return styleAccent.Render(s)
}

// Faint returns a faint-styled string.
func Faint(s string) string {
	return styleFaint.Render(s)
}
