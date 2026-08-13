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

// pct renders a [0,1] rate as "82.3%". Returns a visible error string for
// inputs above 1 rather than panicking (that would blank the whole report).
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

// HTML renders r as a self-contained HTML document.
func (r *Report) HTML(w io.Writer) error {
	return reportHTMLTemplate.Execute(w, r)
}
