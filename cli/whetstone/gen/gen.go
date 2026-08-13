// Package gen writes a skill bundle from an approved spec.
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

// Generate writes a skill bundle for s into outDir.
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
