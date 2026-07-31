// Package gen writes a skill bundle — SKILL.md plus scripts/, references/,
// and assets/ — from an approved spec.SkillSpec. Four model passes (outline,
// body, resources, description) draft the content; a final, deterministic
// assembly step writes it to disk under the same rules the quality linter
// enforces, so a generated bundle passes its own lint by construction.
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

// Generate writes a skill bundle for s into outDir. It runs four gateway
// passes in order — outline, body, resources, description — then assembles
// the result deterministically with no further model call.
//
// Resource paths in the resources pass come from the model and are untrusted
// input: assemble refuses any path that is absolute or escapes the bundle
// directory, rather than silently cleaning it.
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

	b, err := assemble(s, outDir, body.Body, description.Description, resources)
	if err != nil {
		return nil, fmt.Errorf("gen: assembling bundle: %w", err)
	}
	return b, nil
}
