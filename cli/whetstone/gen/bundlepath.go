package gen

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SafeJoin is the bundle-containment check for model-authored paths: a
// resource path a model names is untrusted input.
//
// It resolves rel inside dir, refusing anything that escapes: an
// absolute path, or a traversal that resolves outside dir even when buried
// after the first path segment (e.g. "scripts/../../escape.sh", which does
// not start with ".." and so would slip past a naive prefix check).
func SafeJoin(dir, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path %q", rel)
	}
	target := filepath.Join(dir, rel)
	within, err := filepath.Rel(dir, target)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", rel, err)
	}
	if within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the bundle", rel)
	}
	return target, nil
}
