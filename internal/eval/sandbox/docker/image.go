package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
)

// EnsureBase builds the base image if its tag is absent. It is separate from
// Prepare because the base is shared across every skill and takes minutes,
// while a per-skill layer takes seconds.
func (d *Driver) EnsureBase(ctx context.Context, slim bool) error {
	tag := d.o.BaseTag
	if _, err := d.output(ctx, "image", "inspect", tag); err == nil {
		return nil
	}
	d.o.Logger("building base image %s (first run only)", tag)
	return d.build(ctx, tag, imagespec.BaseDockerfile(slim))
}

// Prepare builds the per-skill layer, or reuses it when its content-addressed
// tag already exists. The tag is the deps digest, so "already built" and
// "identical contents" are the same question.
func (d *Driver) Prepare(ctx context.Context, e sandbox.EnvSpec) (sandbox.ImageRef, error) {
	if e.BaseTag == "" {
		e.BaseTag = d.o.BaseTag
	}
	tag, err := imagespec.Tag(e)
	if err != nil {
		return sandbox.ImageRef{}, err
	}
	digest, err := imagespec.DepsDigest(e)
	if err != nil {
		return sandbox.ImageRef{}, err
	}
	ref := sandbox.ImageRef{Tag: tag, DepsDigest: digest}

	if _, err := d.output(ctx, "image", "inspect", tag); err == nil {
		return ref, nil
	}
	df, err := imagespec.Render(e)
	if err != nil {
		return sandbox.ImageRef{}, err
	}
	if err := d.build(ctx, tag, df); err != nil {
		return sandbox.ImageRef{}, err
	}
	return ref, nil
}

// build runs a Dockerfile against a truly empty build context. An empty
// context is deliberate: a fragment that wants files must COPY them from a
// path the caller mounted, and an implicit context would silently ship
// whatever the working directory happened to hold.
//
// The Dockerfile and the empty context each need their own path: BuildKit
// (the CLI's build backend since Docker 23) refuses "-f - -", where both the
// dockerfile and the context ask to read the same stdin stream at once
// ("can't use stdin for both build context and dockerfile"). A temp file and
// a temp directory sidestep that without giving up the empty-context
// guarantee.
func (d *Driver) build(ctx context.Context, tag, dockerfile string) error {
	dir, err := os.MkdirTemp("", "whetstone-build-")
	if err != nil {
		return fmt.Errorf("docker: building %s: %w", tag, err)
	}
	defer os.RemoveAll(dir)

	dfPath := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dfPath, []byte(dockerfile), 0o600); err != nil {
		return fmt.Errorf("docker: building %s: %w", tag, err)
	}

	emptyCtx, err := os.MkdirTemp("", "whetstone-ctx-")
	if err != nil {
		return fmt.Errorf("docker: building %s: %w", tag, err)
	}
	defer os.RemoveAll(emptyCtx)

	cmd := execCommand(ctx, d.o.Binary, "build", "-t", tag, "-f", dfPath, emptyCtx)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker: building %s: %w\n%s", tag, err, out)
	}
	return nil
}
