package lint

import (
	"os"
	"path/filepath"
	"strings"
)

// SidecarDir is the eval sidecar's directory name, at the bundle root. Every
// eval-only artifact — the compiled contract, the generated suite, its oracle
// and verifier scripts — lives under it, precisely so that "what does not ship"
// is one rule rather than a list of filenames that drifts.
const SidecarDir = "eval"

// SpecFile is the authored spec. It sits beside the bundle rather than inside
// the sidecar, because the spec is the authored artifact rather than eval
// scaffolding — so it needs its own exclusion.
const SpecFile = "spec.yaml"

// archiveSuffix is the extension of a packed bundle.
const archiveSuffix = ".tar.gz"

// Excluded reports whether rel — a slash-separated path relative to the bundle
// root, naming a file or a directory — is authoring scaffolding rather than
// shipped skill content.
//
// This set is defined here, and only here, because lint is the layer that has
// to know what a bundle is: it walks the whole directory, and every layer it
// runs (spec conformance, quality, the security scanner) judges what it finds
// as if that were shipped skill content. `pack` consumes the same predicate
// rather than restating it, so the two cannot disagree about what a bundle is.
// They did: lint read the sidecar's model-authored oracle scripts through the
// scanner and reported an oracle that provisions its sandbox as an attack,
// while pack — which strips the sidecar — was gated on that same lint and so
// could never ship the bundle. `pack .` had the mirror-image problem: its own
// archive landed in the bundle, and the next lint read gzip bytes and failed
// on invalid UTF-8, so packing worked exactly once.
//
// Excluding the sidecar from the scanner is not a hole: nothing under it is
// ever packed, so nothing under it reaches an installer. That is also why the
// rule is anchored at the bundle root — a "references/eval/" directory is
// ordinary shipped content and is scanned like any other.
func Excluded(rel string) bool {
	switch {
	case rel == SidecarDir || strings.HasPrefix(rel, SidecarDir+"/"):
		return true
	case rel == SpecFile:
		return true
	case !strings.Contains(rel, "/") && strings.HasSuffix(rel, archiveSuffix):
		// A packed archive sitting at the bundle root: `pack .` writes one
		// there by default, and it is a build output, not bundle content.
		return true
	}
	return false
}

// bundleRel converts an absolute (or walk-produced) path into the
// slash-separated, bundle-relative form Excluded expects. It reports !ok when
// path is not under bundleDir, so a caller treats it as ordinary content
// rather than silently excluding something it could not place.
func bundleRel(bundleDir, path string) (string, bool) {
	rel, err := filepath.Rel(bundleDir, path)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

// excludedWalkEntry answers, for one filepath.Walk callback, whether the entry
// is excluded and what the callback must return if it is: filepath.SkipDir for
// a directory, so its whole subtree is skipped in one step, and nil for a file.
func excludedWalkEntry(bundleDir, path string, info os.FileInfo) (bool, error) {
	rel, ok := bundleRel(bundleDir, path)
	if !ok || !Excluded(rel) {
		return false, nil
	}
	if info != nil && info.IsDir() {
		return true, filepath.SkipDir
	}
	return true, nil
}
