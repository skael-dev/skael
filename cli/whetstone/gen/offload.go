package gen

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/skael-dev/skael/internal/eval/lint"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// topHeadingLine matches a level-2 markdown heading exactly.
var topHeadingLine = regexp.MustCompile(`^## (.+)$`)

var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// offloadTargetBytes targets just under the budget — an aggressive target
// strips orientation sections from bodies whose procedure alone approaches it.
var offloadTargetBytes = int(0.98 * float64(lint.MaxBodyApproxTokens*4))

// bodySection is one "## " slice. The first section (before any heading) has
// an empty heading and is never an offload candidate.
type bodySection struct {
	heading string
	lines   []string
}

func splitTopSections(body string) []bodySection {
	lines := strings.Split(body, "\n")

	var starts []int
	var headings []string
	for i, line := range lines {
		if m := topHeadingLine.FindStringSubmatch(line); m != nil {
			starts = append(starts, i)
			headings = append(headings, strings.TrimSpace(m[1]))
		}
	}

	var sections []bodySection
	firstHeading := len(lines)
	if len(starts) > 0 {
		firstHeading = starts[0]
	}
	if firstHeading > 0 {
		sections = append(sections, bodySection{lines: lines[:firstHeading]})
	}
	for i, start := range starts {
		end := len(lines)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		sections = append(sections, bodySection{heading: headings[i], lines: lines[start:end]})
	}
	return sections
}

func joinSections(sections []bodySection) string {
	var lines []string
	for _, sec := range sections {
		lines = append(lines, sec.lines...)
	}
	body := strings.Join(lines, "\n")
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return body
}

// offloadOverBudgetSections moves the largest offloadable sections into
// references/ files until the body fits offloadTargetBytes. Largest-first
// keeps short orientation sections in the body.
func offloadOverBudgetSections(body string, existing []resourceFile) (string, []resourceFile) {
	if len(body) <= offloadTargetBytes {
		return body, nil
	}

	sections := splitTopSections(body)

	var candidates []int
	for i, sec := range sections {
		if sec.heading != "" && isOffloadable(sec) {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return body, nil
	}

	sort.Slice(candidates, func(a, b int) bool {
		return len(strings.Join(sections[candidates[a]].lines, "\n")) > len(strings.Join(sections[candidates[b]].lines, "\n"))
	})

	used := map[string]bool{}
	for _, f := range existing {
		if slug, ok := strings.CutPrefix(f.Path, "references/"); ok {
			used[strings.TrimSuffix(slug, ".md")] = true
		}
	}

	var refs []resourceFile
	for _, idx := range candidates {
		if len(refs) >= spec.MaxReferences || len(joinSections(sections)) <= offloadTargetBytes {
			break
		}

		sec := sections[idx]
		slug := uniqueSlug(sec.heading, used)
		used[slug] = true
		refPath := "references/" + slug + ".md"

		content := strings.TrimRight(strings.Join(sec.lines, "\n"), "\n") + "\n"
		refs = append(refs, resourceFile{Path: refPath, Content: content})

		sections[idx] = bodySection{
			heading: sec.heading,
			lines: []string{
				"## " + sec.heading,
				"",
				fmt.Sprintf("See [%s](%s).", sec.heading, refPath),
				"",
			},
		}
	}

	if len(refs) == 0 {
		return body, nil
	}
	return joinSections(sections), refs
}

// stepSubheading matches "### Step N" subheadings in any section.
var stepSubheading = regexp.MustCompile(`(?im)^#{3,6}\s*step\b`)

// orientationHeadingWords name sections that must stay in the body — behind
// a link, an agent cannot tell what the skill is without fetching another file.
var orientationHeadingWords = []string{"overview", "when to use", "purpose"}

// isOffloadable requires a section to be positively identified as reference
// material. The structural check can only veto — absence of evidence must not
// move a procedure section whose steps use subheadings instead of a list.
func isOffloadable(sec bodySection) bool {
	if !lint.IsDeclarativeSection(sec.heading) {
		return false
	}
	lower := strings.ToLower(sec.heading)
	for _, w := range orientationHeadingWords {
		if strings.Contains(lower, w) {
			return false
		}
	}
	return !stepSubheading.MatchString(strings.Join(sec.lines, "\n"))
}

func slugify(heading string) string {
	s := slugNonAlnum.ReplaceAllString(strings.ToLower(heading), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "reference"
	}
	return s
}

func uniqueSlug(heading string, used map[string]bool) string {
	base := slugify(heading)
	slug := base
	for n := 2; used[slug]; n++ {
		slug = fmt.Sprintf("%s-%d", base, n)
	}
	return slug
}
