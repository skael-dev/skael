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
	"shortref": suite.ShortRef,
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

// HTML renders r as a single self-contained HTML document: no external
// stylesheet, script, font, or image, so the report keeps rendering offline
// and never signals a third party which skills a team evaluated. All
// model-authored and agent-authored text (task prompts, judge evidence,
// violation evidence) goes through html/template's default escaping.
func (r *Report) HTML(w io.Writer) error {
	return reportHTMLTemplate.Execute(w, r)
}
