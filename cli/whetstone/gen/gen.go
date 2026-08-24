// Package gen writes a skill bundle from an approved spec.
package gen

import (
	"context"
	"fmt"
	"sync"

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
	// Three branches, started together. Only the body depends on the outline;
	// the resources and the description read the spec alone, so the critical
	// path is outline plus body.
	var (
		body        bodyRes
		resources   resourcesRes
		description descriptionRes
		bodyErr     error
		resErr      error
		descErr     error
		wg          sync.WaitGroup
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		outline, err := runOutline(ctx, g, s)
		if err != nil {
			bodyErr = fmt.Errorf("gen: outline pass: %w", err)
			return
		}
		body, err = runBody(ctx, g, s, outline)
		if err != nil {
			bodyErr = fmt.Errorf("gen: body pass: %w", err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		if resources, err = runResources(ctx, g, s); err != nil {
			resErr = fmt.Errorf("gen: resources pass: %w", err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		if description, err = runDescription(ctx, g, s); err != nil {
			descErr = fmt.Errorf("gen: description pass: %w", err)
		}
	}()
	wg.Wait()

	for _, err := range []error{bodyErr, resErr, descErr} {
		if err != nil {
			return nil, err
		}
	}

	bodyText, descText := body.Body, description.Description
	b, err := reviseUntilClean(ctx, g, s, outDir, &bodyText, &descText, resources)
	if err != nil {
		return nil, err
	}
	return b, nil
}
