package report

import "fmt"

// maxUnevaluableDetail bounds how many distinct reasons a report lists. The
// count in Report.Unevaluable is always exact; this only bounds the prose.
const maxUnevaluableDetail = 25

// dedupeDetail collapses repeated messages into "msg (×N)" and caps the
// result. Unevaluable checks are counted per (rule × event) pair, so one
// systematic cause produces hundreds of identical lines, burying the fact a
// reader needs: how many *different* things went wrong.
//
// Ordered by first appearance, not frequency — sorting by count would reorder
// the list every run for the same underlying cause.
func dedupeDetail(details []string, max int) []string {
	if len(details) == 0 {
		return nil
	}

	counts := make(map[string]int, len(details))
	order := make([]string, 0, len(details))
	for _, d := range details {
		if _, seen := counts[d]; !seen {
			order = append(order, d)
		}
		counts[d]++
	}

	truncated := 0
	if max > 0 && len(order) > max {
		truncated = len(order) - max
		order = order[:max]
	}

	out := make([]string, 0, len(order)+1)
	for _, d := range order {
		if n := counts[d]; n > 1 {
			out = append(out, fmt.Sprintf("%s (×%d)", d, n))
			continue
		}
		out = append(out, d)
	}
	if truncated > 0 {
		// Say what was dropped. A silently truncated list reads as a complete
		// one, which is how a reader concludes they have seen every cause.
		out = append(out, fmt.Sprintf("… and %d further distinct reason(s) not shown", truncated))
	}
	return out
}
