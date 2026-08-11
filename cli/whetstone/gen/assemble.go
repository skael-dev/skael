package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skael-dev/skael/internal/eval/spec"
	"gopkg.in/yaml.v3"
)

// Permissions for files written into the bundle. Scripts are executable;
// everything else, including SKILL.md itself, is not.
const (
	scriptMode = os.FileMode(0o755)
	fileMode   = os.FileMode(0o644)
	dirMode    = os.FileMode(0o755)
)

// frontmatter is the YAML document written at the top of SKILL.md. It is a
// struct rather than a map so yaml.Marshal emits fields in this fixed order
// regardless of Go map iteration order.
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// safeJoin resolves rel inside dir, refusing anything that escapes. Resource
// paths come from the model, so they are untrusted input: an absolute path or a
// traversal must be an error rather than something quietly cleaned up.
//
// The containment check itself lives in bundlepath, shared with the repair
// package's proposal paths, so there is exactly one implementation of the
// rule; this wrapper only adds gen's error context.
func safeJoin(dir, rel string) (string, error) {
	target, err := SafeJoin(dir, rel)
	if err != nil {
		return "", fmt.Errorf("gen: resource %w", err)
	}
	return target, nil
}

// assemble deterministically writes the bundle to disk: SKILL.md
// (frontmatter + body), then every resource file, and returns a Bundle
// listing everything written. No gateway call happens here — every input is
// already in hand from the four passes.
func assemble(s *spec.SkillSpec, outDir, body, description string, resources resourcesRes) (*Bundle, error) {
	dir := filepath.Join(outDir, s.DirName())
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("gen: creating bundle directory: %w", err)
	}

	var written []string

	skillPath, err := writeSkillMD(dir, s, body, description)
	if err != nil {
		return nil, err
	}
	written = append(written, skillPath)

	for _, f := range resources.Files {
		rel, err := writeResource(dir, f.Path, f.Content)
		if err != nil {
			return nil, err
		}
		written = append(written, rel)
	}

	sort.Strings(written)
	return &Bundle{Dir: dir, Files: written}, nil
}

// writeSkillMD renders and writes SKILL.md, returning its path relative to
// dir.
func writeSkillMD(dir string, s *spec.SkillSpec, body, description string) (string, error) {
	fm := frontmatter{
		Name:        s.DirName(),
		Description: description,
	}
	yamlBytes, err := yaml.Marshal(fm)
	if err != nil {
		return "", fmt.Errorf("gen: marshalling frontmatter: %w", err)
	}

	var content strings.Builder
	content.WriteString("---\n")
	content.Write(yamlBytes)
	content.WriteString("---\n\n")
	content.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		content.WriteString("\n")
	}

	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content.String()), fileMode); err != nil {
		return "", fmt.Errorf("gen: writing SKILL.md: %w", err)
	}
	return "SKILL.md", nil
}

// RewriteDescription replaces the description in a bundle's SKILL.md
// frontmatter and leaves the body untouched.
//
// The tuner changes one field. A full regeneration spends several model
// calls and rewrites prose nobody asked to change. The frontmatter is
// re-marshalled instead. The body is copied through verbatim.
func RewriteDescription(bundleDir, description string) error {
	path := filepath.Join(bundleDir, "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("gen: reading %s: %w", path, err)
	}

	const sep = "---\n"
	body := string(raw)
	if !strings.HasPrefix(body, sep) {
		return fmt.Errorf("gen: %s has no frontmatter", path)
	}
	rest := body[len(sep):]
	end := strings.Index(rest, "\n"+sep)
	if end < 0 {
		return fmt.Errorf("gen: %s has an unterminated frontmatter block", path)
	}

	var fm frontmatter
	if err := yaml.Unmarshal([]byte(rest[:end+1]), &fm); err != nil {
		return fmt.Errorf("gen: parsing the frontmatter of %s: %w", path, err)
	}
	fm.Description = description

	out, err := yaml.Marshal(fm)
	if err != nil {
		return fmt.Errorf("gen: marshalling frontmatter: %w", err)
	}

	var content strings.Builder
	content.WriteString(sep)
	content.Write(out)
	content.WriteString(sep)
	content.WriteString(rest[end+1+len(sep):])

	if err := os.WriteFile(path, []byte(content.String()), fileMode); err != nil {
		return fmt.Errorf("gen: writing %s: %w", path, err)
	}
	return nil
}

// writeResource writes one model-authored resource file through safeJoin and
// returns its path relative to dir. Files under scripts/ are written
// executable; everything else is not.
func writeResource(dir, relPath, content string) (string, error) {
	target, err := safeJoin(dir, relPath)
	if err != nil {
		return "", fmt.Errorf("gen: writing resource: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(target), dirMode); err != nil {
		return "", fmt.Errorf("gen: creating resource directory for %q: %w", relPath, err)
	}

	mode := fileMode
	if isUnderScripts(relPath) {
		mode = scriptMode
	}
	if err := os.WriteFile(target, []byte(content), mode); err != nil {
		return "", fmt.Errorf("gen: writing resource %q: %w", relPath, err)
	}
	return filepath.ToSlash(filepath.Clean(relPath)), nil
}

// isUnderScripts reports whether a bundle-relative path is under scripts/.
func isUnderScripts(relPath string) bool {
	clean := filepath.ToSlash(filepath.Clean(relPath))
	return clean == "scripts" || strings.HasPrefix(clean, "scripts/")
}
