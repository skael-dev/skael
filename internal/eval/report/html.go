package report

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"

	"github.com/skael-dev/skael/internal/eval/suite"
)

//go:embed templates/report.html.tmpl
var reportHTMLSource string

var reportHTMLTemplate = template.Must(template.New("report.html.tmpl").Funcs(template.FuncMap{
	"pct":      pct,
	"round1":   round1,
	"gfmt":     gfmt,
	"shortref": suite.ShortRef,
	"deref":    deref,
}).Parse(reportHTMLSource))

// pct renders a [0,1] rate as a one-decimal percentage, e.g. 0.823 -> "82.3%".
//
// It refuses an input above 1: pct is only ever fed rates, and drift.Agg's
// Mean/Worst/Sigma are already on a 0-100 scale (means of Adherence, not
// rates) — feeding one through pct silently produces something like
// "8750.0%". Refusing means returning a visible, malformed-looking string
// rather than panicking (a template execution failure would blank the whole
// report, worse than one wrong-looking cell) or silently clamping (which
// would hide the caller bug the same way the original defect did).
func pct(rate float64) string {
	if rate > 1 {
		return "invalid pct input"
	}
	return fmt.Sprintf("%.1f%%", rate*100)
}

// round1 renders a float to one decimal place.
func round1(v float64) string {
	return fmt.Sprintf("%.1f", v)
}

// gfmt renders a float with %g: its shortest exact decimal representation.
// Used where round1's one decimal would quantize away a meaningful
// borderline, e.g. a judge margin read against a 0.15 threshold.
func gfmt(v float64) string {
	return fmt.Sprintf("%g", v)
}

// deref renders the value behind a *float64 at two decimal places, or the
// empty string for a nil pointer. Callers guard nil with {{if}} before
// reaching for the value itself; this exists so a template pipeline never has
// to dereference a pointer directly.
//
// Two decimals rather than round1 because κ and the robustness gap are read at
// more than one decimal of precision (e.g. κ = 0.41) and rounding to one would
// discard that. It was %g before, which is the shortest *exact* decimal for a
// float64 and therefore prints all seventeen significant digits of a computed
// ratio — a real report rendered "judge κ = 0.45121951219512196", which
// overshot this comment's own stated intent by fifteen digits.
//
// deref itself is unitless — it renders both a [0,1] κ and a [0,100]-point
// robustness gap identically. The template call site is responsible for
// labelling the unit (the robustness gap's call site appends "points") so
// the two are never confused for the same scale.
func deref(v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%.2f", *v)
}

// HTML renders r as a single self-contained HTML document: no external
// stylesheet, script, font, or image, so the report keeps rendering offline
// and never signals a third party which skills a team evaluated. All
// model-authored and agent-authored text (task prompts, judge evidence,
// violation evidence) goes through html/template's default escaping.
func (r *Report) HTML(w io.Writer) error {
	return reportHTMLTemplate.Execute(w, r)
}
