package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/skill"
)

// specName matches the Agent Skills spec's name format: 1-64 lowercase
// kebab-case characters. This is stricter than the registry's own name rule,
// which additionally allows colons for namespacing — a namespaced registry
// name is not a spec-compliant skill name.
//
// This is a third copy of the same character class: internal/skill's
// specKebab and internal/eval/spec's specName are both unexported, so neither
// can be imported here. internal/eval/spec.SkillSpec.Validate can't be
// reused as a probe either — it validates SkillSpec.DirName(), which strips
// everything up to and including a ':' before checking, so it silently
// accepts "superpowers:brainstorming" (the exact registry-namespaced input
// this rule exists to reject). If the character class ever changes, check
// internal/skill/archive.go's specKebab and internal/eval/spec/validate.go's
// specName too.
var specName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// mdLink extracts the target of a markdown link: [text](target).
var mdLink = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)

// externalLink matches link targets that are not resolved on disk: full URLs,
// mailto links, and same-document anchors. Linting is offline and
// deterministic, so these are never checked for existence.
var externalLink = regexp.MustCompile(`^(https?:|mailto:|#)`)

// stripLinkSuffix removes a trailing #fragment or ?query from a relative
// link target before it is resolved on disk — "doc.md#section" and
// "doc.md?v=2" both link to doc.md, and neither suffix is part of a
// filesystem path.
func stripLinkSuffix(target string) string {
	if i := strings.IndexAny(target, "#?"); i >= 0 {
		return target[:i]
	}
	return target
}

// knownFrontmatterKeys are the top-level keys the Agent Skills spec and
// skael's registry both understand. Anything else is a warning, not an
// error: an unrecognized key isn't invalid, just unactioned.
var knownFrontmatterKeys = map[string]bool{
	"name":          true,
	"description":   true,
	"license":       true,
	"compatibility": true,
	"author":        true,
	"tags":          true,
	"metadata":      true,
	"version":       true,
	"display_name":  true,
}

// Conformance checks a bundle against the Agent Skills spec format. It
// delegates frontmatter validation to skill.ParseFrontmatter and
// skill.ValidateSpec rather than restating their rules, so a bundle cannot
// pass this check and then fail the registry's own compliance check at
// publish time.
func Conformance(bundleDir string) ([]Finding, error) {
	var findings []Finding

	skillPath := filepath.Join(bundleDir, "SKILL.md")

	// Lstat rather than Stat: a symlinked SKILL.md must not be read, since its
	// target can point anywhere the process can reach. Lstat also reports a
	// dangling symlink's own entry (not an error following it), which is
	// treated the same as any other symlink below.
	skillInfo, err := os.Lstat(skillPath)
	if err != nil {
		if os.IsNotExist(err) {
			findings = append(findings, Finding{
				Rule:     "missing-skill-md",
				Severity: SeverityError,
				File:     "SKILL.md",
				Message:  "bundle has no SKILL.md",
			})
			return findings, nil
		}
		return nil, fmt.Errorf("lint: reading SKILL.md: %w", err)
	}
	if skillInfo.Mode()&os.ModeSymlink != 0 {
		findings = append(findings, symlinkFinding(bundleDir, skillPath))
		return findings, nil
	}

	raw, err := os.ReadFile(skillPath)
	if err != nil {
		return nil, fmt.Errorf("lint: reading SKILL.md: %w", err)
	}

	symlinkFindings, err := checkSymlinks(bundleDir)
	if err != nil {
		return nil, err
	}
	findings = append(findings, symlinkFindings...)

	archiveFindings, err := checkRootArchives(bundleDir)
	if err != nil {
		return nil, err
	}
	findings = append(findings, archiveFindings...)

	utf8Findings, err := checkUTF8(bundleDir)
	if err != nil {
		return nil, err
	}
	findings = append(findings, utf8Findings...)

	content := string(raw)
	dirName := filepath.Base(bundleDir)

	fm, _, err := skill.ParseFrontmatter(content)
	if err != nil {
		findings = append(findings, Finding{
			Rule:     "frontmatter-invalid",
			Severity: SeverityError,
			File:     "SKILL.md",
			Message:  err.Error(),
		})
	}

	findings = append(findings, checkDescription(content, fm)...)
	findings = append(findings, checkName(content, fm, dirName)...)
	findings = append(findings, checkSpecDelegation(fm, dirName)...)
	findings = append(findings, checkLinks(bundleDir, content)...)

	refFindings, err := checkReferenceDepth(bundleDir)
	if err != nil {
		return nil, err
	}
	findings = append(findings, refFindings...)

	findings = append(findings, checkCompatibility(content, fm)...)
	findings = append(findings, checkUnknownKeys(content, fm)...)

	return findings, nil
}

// checkSymlinks flags every file symlink in the bundle without following it.
// skill.Unpack rejects symlinks outright when a bundle is packed for
// publishing, so a symlinked file here is one that will be rejected anyway —
// surfacing it during lint means the author learns about it while working on
// the source tree, not after a failed publish. filepath.Walk already never
// traverses into a symlinked directory, so this only needs to check the
// entries it does visit.
func checkSymlinks(bundleDir string) ([]Finding, error) {
	var findings []Finding
	err := filepath.Walk(bundleDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if skip, ret := excludedWalkEntry(bundleDir, path, info); skip {
			return ret
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		findings = append(findings, symlinkFinding(bundleDir, path))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return findings, nil
}

// symlinkFinding builds the finding for a symlinked bundle entry at path.
func symlinkFinding(bundleDir, path string) Finding {
	rel, err := filepath.Rel(bundleDir, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	return Finding{
		Rule:     "symlink-not-allowed",
		Severity: SeverityError,
		File:     rel,
		Message:  fmt.Sprintf("%s is a symlink; skill.Unpack rejects symlinks when the bundle is packed for publishing", rel),
	}
}

// checkUTF8 flags any file in the bundle whose bytes are not valid UTF-8. It
// never follows a symlink — checkSymlinks already reports those — since
// reading through one would describe content that isn't part of the bundle.
func checkUTF8(bundleDir string) ([]Finding, error) {
	var findings []Finding
	err := filepath.Walk(bundleDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if skip, ret := excludedWalkEntry(bundleDir, path, info); skip {
			return ret
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !utf8.Valid(b) {
			rel, err := filepath.Rel(bundleDir, path)
			if err != nil {
				return err
			}
			findings = append(findings, Finding{
				Rule:     "invalid-utf8",
				Severity: SeverityError,
				File:     filepath.ToSlash(rel),
				Message:  "file contents are not valid UTF-8",
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return findings, nil
}

// checkDescription enforces that a description is present and within the
// spec's 1024-byte limit.
func checkDescription(content string, fm map[string]interface{}) []Finding {
	descRaw, hasDesc := fm["description"]
	desc, _ := descRaw.(string)

	switch {
	case !hasDesc || desc == "":
		return []Finding{{
			Rule:     "description-missing",
			Severity: SeverityError,
			File:     "SKILL.md",
			Message:  "frontmatter has no description",
		}}
	case len(desc) > spec.MaxDescription:
		return []Finding{{
			Rule:     "description-too-long",
			Severity: SeverityError,
			File:     "SKILL.md",
			Line:     frontmatterKeyLine(content, "description"),
			Message:  fmt.Sprintf("description is %d bytes, over the %d-byte limit", len(desc), spec.MaxDescription),
		}}
	}
	return nil
}

// checkName enforces the spec's name format and that the frontmatter name
// matches the bundle's directory name.
func checkName(content string, fm map[string]interface{}, dirName string) []Finding {
	nameRaw, hasName := fm["name"]
	name, _ := nameRaw.(string)
	if !hasName || name == "" {
		return nil
	}

	var findings []Finding
	line := frontmatterKeyLine(content, "name")

	if !specName.MatchString(name) || len(name) > spec.MaxName {
		findings = append(findings, Finding{
			Rule:     "name-not-spec-compliant",
			Severity: SeverityError,
			File:     "SKILL.md",
			Line:     line,
			Message:  fmt.Sprintf("name %q does not match the spec format (1-%d lowercase kebab-case)", name, spec.MaxName),
		})
	}

	if name != dirName {
		findings = append(findings, Finding{
			Rule:     "name-dir-mismatch",
			Severity: SeverityError,
			File:     "SKILL.md",
			Line:     line,
			Message:  fmt.Sprintf("frontmatter name %q does not match bundle directory %q", name, dirName),
		})
	}

	return findings
}

// checkSpecDelegation runs the registry's own spec validator and surfaces its
// findings, so this layer cannot drift from what publish-time compliance
// checking accepts.
func checkSpecDelegation(fm map[string]interface{}, dirName string) []Finding {
	var findings []Finding

	sv := skill.ValidateSpec(fm, dirName)
	for _, w := range sv.Warnings {
		findings = append(findings, Finding{
			Rule:     "spec-warning",
			Severity: SeverityWarn,
			File:     "SKILL.md",
			Message:  w,
		})
	}

	if sv.Compliance == "none" {
		findings = append(findings, Finding{
			Rule:     "spec-noncompliant",
			Severity: SeverityError,
			File:     "SKILL.md",
			Message:  "frontmatter does not satisfy the Agent Skills spec",
		})
	}

	return findings
}

// checkLinks resolves every relative markdown link against bundleDir and
// flags targets that don't exist. Links to a scheme (http, https, mailto) or
// a same-document anchor are skipped: linting never makes a network call.
func checkLinks(bundleDir, content string) []Finding {
	var findings []Finding
	for i, line := range strings.Split(content, "\n") {
		for _, m := range mdLink.FindAllStringSubmatch(line, -1) {
			target := m[1]
			if externalLink.MatchString(target) {
				continue
			}
			resolved := filepath.Join(bundleDir, filepath.FromSlash(stripLinkSuffix(target)))
			if _, err := os.Stat(resolved); err != nil {
				findings = append(findings, Finding{
					Rule:     "broken-link",
					Severity: SeverityError,
					File:     "SKILL.md",
					Line:     i + 1,
					Message:  fmt.Sprintf("linked path %q does not exist", target),
				})
			}
		}
	}
	return findings
}

// checkReferenceDepth flags files under references/ nested more than one
// directory deep.
func checkReferenceDepth(bundleDir string) ([]Finding, error) {
	refDir := filepath.Join(bundleDir, "references")
	info, err := os.Stat(refDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}

	var findings []Finding
	err = filepath.Walk(refDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(refDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.Count(rel, "/") > 1 {
			findings = append(findings, Finding{
				Rule:     "reference-too-deep",
				Severity: SeverityError,
				File:     "references/" + rel,
				Message:  fmt.Sprintf("references/%s is nested more than one directory deep", rel),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return findings, nil
}

// checkCompatibility enforces the 500-byte limit on the compatibility field.
func checkCompatibility(content string, fm map[string]interface{}) []Finding {
	compat, ok := fm["compatibility"].(string)
	if !ok || len(compat) <= 500 {
		return nil
	}
	return []Finding{{
		Rule:     "compatibility-too-long",
		Severity: SeverityError,
		File:     "SKILL.md",
		Line:     frontmatterKeyLine(content, "compatibility"),
		Message:  fmt.Sprintf("compatibility is %d bytes, over the 500-byte limit", len(compat)),
	}}
}

// checkUnknownKeys warns about top-level frontmatter keys the spec and
// registry don't recognize. Unlike the other checks, an unrecognized key
// isn't a spec violation — it's just unactioned — so it's a warning.
func checkUnknownKeys(content string, fm map[string]interface{}) []Finding {
	keys := make([]string, 0, len(fm))
	for k := range fm {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var findings []Finding
	for _, k := range keys {
		if knownFrontmatterKeys[k] {
			continue
		}
		findings = append(findings, Finding{
			Rule:     "unknown-key",
			Severity: SeverityWarn,
			File:     "SKILL.md",
			Line:     frontmatterKeyLine(content, k),
			Message:  fmt.Sprintf("unknown frontmatter key %q", k),
		})
	}
	return findings
}

// frontmatterKeyLine returns the 1-indexed line number of a top-level
// frontmatter key, or 0 if content has no frontmatter or the key isn't
// present in it.
func frontmatterKeyLine(content, key string) int {
	lines := strings.Split(content, "\n")
	inFrontmatter := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			break
		}
		if inFrontmatter && strings.HasPrefix(trimmed, key+":") {
			return i + 1
		}
	}
	return 0
}
