package gen

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/skael-dev/skael/internal/eval/lint"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// topHeadingLine matches a level-2 markdown heading exactly — "### " does not
// match, since the character after "##" must be a space.
var topHeadingLine = regexp.MustCompile(`^## (.+)$`)

// slugNonAlnum is everything a slug collapses to a single '-'.
var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// offloadTargetBytes is the body size the offload pass moves content out to —
// just under the budget rather than well under it. An aggressive target does
// not fail safe: when a skill's procedure alone approaches the budget, chasing
// a number the remaining candidates cannot reach empties the body of its
// overview and orientation sections too, which is worse than being close to
// the limit.
var offloadTargetBytes = int(0.98 * float64(lint.MaxBodyApproxTokens*4))

// bodySection is one top-level slice of a body: everything from a "## "
// heading line up to (not including) the next one. The first section, before
// any heading, has an empty heading and is never an offload candidate.
type bodySection struct {
	heading string
	lines   []string
}

// splitTopSections splits body at every top-level "## " heading.
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

// joinSections reassembles sections back into a single body string.
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

// offloadOverBudgetSections moves the body's largest offloadable sections
// (see isOffloadable) into references/<slug>.md files until the body fits
// offloadTargetBytes or spec.MaxReferences files have been produced,
// whichever comes first. It is pure and makes no gateway call — the model
// cannot count its own tokens, so closing a body-budget gap by asking it to
// try harder converges slowly if at all; moving text costs nothing and is
// guaranteed.
//
// Largest-first is what keeps the short orientation sections (Overview, When
// to use) in the body: the loop stops as soon as the body fits, so they are
// only ever reached last.
//
// existing is the resource plan already destined for the bundle — offloaded
// slugs must not collide with a reference the model already planned to
// write. A body that is under budget, or made entirely of procedure, is a
// no-op rather than something to mangle by moving steps out of the way.
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

// stepSubheading matches a "### Step 3 — …" subheading. Skills write their
// procedure this way as often as they use a numbered list, and a section
// carrying these is a procedure whatever its own heading says.
var stepSubheading = regexp.MustCompile(`(?im)^#{3,6}\s*step\b`)

// orientationHeadingWords name sections that must stay in the body even
// though the linter treats them as declarative. They answer "what is this and
// when does it apply" — behind a link, an agent reading SKILL.md cannot tell
// what the skill is without fetching a second file, which is the one thing
// progressive disclosure must not cost.
var orientationHeadingWords = []string{"overview", "when to use", "purpose"}

// isOffloadable reports whether a section may leave the body. The steps are
// the skill's value and stay; everything else is reference material.
//
// A section must be positively identified as reference material to move —
// never merely fail a procedure test. Absence of evidence is not fail-safe
// here: an early version treated "carries no numbered list" as offloadable
// and moved a whole 18KB Workflow section, whose steps were "### Step 1 —"
// subheadings, out of the body. Losing the procedure is far worse than
// leaving the body over budget, so the allow-list decides and the structural
// check can only veto.
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

// slugify lowercases a heading and collapses everything but letters and
// digits to a single '-', producing a bare filename-safe slug.
func slugify(heading string) string {
	s := slugNonAlnum.ReplaceAllString(strings.ToLower(heading), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "reference"
	}
	return s
}

// uniqueSlug returns heading's slug, or that slug suffixed with an
// incrementing number if it's already in used — two sections titled
// "Notes" must not both become references/notes.md.
func uniqueSlug(heading string, used map[string]bool) string {
	base := slugify(heading)
	slug := base
	for n := 2; used[slug]; n++ {
		slug = fmt.Sprintf("%s-%d", base, n)
	}
	return slug
}
