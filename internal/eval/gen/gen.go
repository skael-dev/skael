// Package gen writes a skill bundle — SKILL.md plus scripts/, references/,
// and assets/ — from an approved spec.SkillSpec. Four model passes (outline,
// body, resources, description) draft the content; assembly writes it to
// disk, then a lint-and-revise loop (revise.go) asks the body or description
// pass to fix whatever lint.Run finds, up to two attempts. The bundle is
// always returned with a nil error — the CLI's own lint gate, not this loop,
// decides whether generation succeeded.
package gen

import (
	"context"
	"fmt"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// Bundle describes a skill bundle written to disk.
type Bundle struct {
	// Dir is the bundle's root directory (outDir/spec.DirName()).
	Dir string
	// Files lists every path written, relative to Dir, sorted.
	Files []string
}

// Generate writes a skill bundle for s into outDir. It runs the outline and
// body passes, then one resources-pass call per planned resource file (see
// runResources), then the description pass, then assembles the result and
// runs reviseUntilClean to fix what lint finds.
//
// Resource paths come from the approved spec, not the model: only a file's
// content is requested per call. assemble still refuses any path that is
// absolute or escapes the bundle directory, as defense in depth.
func Generate(ctx context.Context, g llm.Gateway, s *spec.SkillSpec, outDir string) (*Bundle, error) {
	outline, err := runOutline(ctx, g, s)
	if err != nil {
		return nil, fmt.Errorf("gen: outline pass: %w", err)
	}

	body, err := runBody(ctx, g, s, outline)
	if err != nil {
		return nil, fmt.Errorf("gen: body pass: %w", err)
	}

	resources, err := runResources(ctx, g, s)
	if err != nil {
		return nil, fmt.Errorf("gen: resources pass: %w", err)
	}

	description, err := runDescription(ctx, g, s)
	if err != nil {
		return nil, fmt.Errorf("gen: description pass: %w", err)
	}

	bodyText, descText := body.Body, description.Description
	b, err := reviseUntilClean(ctx, g, s, outDir, &bodyText, &descText, resources)
	if err != nil {
		return nil, err
	}
	return b, nil
}
