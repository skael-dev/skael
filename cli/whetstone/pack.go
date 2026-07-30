package whetstone

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/lint"
	"github.com/skael-dev/skael/internal/ui"
)

// evalDirName is the sidecar directory pack removes. It matches
// store.EvalDir's last element: everything eval-only lives under it precisely
// so that stripping it is one rule rather than a list of filenames that drifts.
const evalDirName = "eval"

// specFileName is the authored spec. It sits beside the bundle rather than
// inside the sidecar, so it needs its own exclusion: an installer has no use
// for it and it describes the skill's internals.
const specFileName = "spec.yaml"

var packOutput string

var packCmd = &cobra.Command{
	Use:   "pack <skill|path>",
	Short: "Write a spec-valid archive with the eval sidecar stripped",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveBundle(args[0])
		if err != nil {
			return err
		}

		out := packOutput
		if out == "" {
			abs, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			out = filepath.Base(abs) + ".tar.gz"
		}

		if err := RunPack(dir, out); err != nil {
			return err
		}
		if ui.JSONMode {
			return ui.PrintJSON(map[string]string{"bundle": dir, "archive": out})
		}
		ui.Success("wrote %s", out)
		return nil
	},
}

// RunPack lints bundleDir and, if it has no errors, writes a tar.gz of it to
// outPath with the eval sidecar and the spec removed.
//
// Linting first is not a convenience: an archive built from a bundle that
// fails lint installs and then fails at use time, a long way from the cause.
func RunPack(bundleDir, outPath string) error {
	res, err := lint.Run(bundleDir)
	if err != nil {
		return err
	}
	if res.HasErrors() {
		return fmt.Errorf("%s fails lint with %d error(s); run `whetstone lint %s` and fix them before packing",
			bundleDir, res.Errors(), bundleDir)
	}

	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("whetstone pack: %w", err)
	}
	defer func() { _ = f.Close() }()

	gzw := gzip.NewWriter(f)
	tw := tar.NewWriter(gzw)

	if err := writeBundleEntries(tw, bundleDir); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("whetstone pack: closing tar: %w", err)
	}
	if err := gzw.Close(); err != nil {
		return fmt.Errorf("whetstone pack: closing gzip: %w", err)
	}
	return f.Close()
}

// writeBundleEntries walks bundleDir and writes every regular file that is not
// part of the eval sidecar into tw.
func writeBundleEntries(tw *tar.Writer, bundleDir string) error {
	return filepath.Walk(bundleDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(bundleDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}

		if info.IsDir() {
			if rel == evalDirName {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			// Symlinks and devices are rejected by skill.Unpack anyway, and
			// lint already reports a symlinked bundle entry as an error.
			return nil
		}
		if rel == specFileName {
			return nil
		}

		// The archive name is derived from a filesystem walk, so it should
		// already be a clean relative path. It is checked anyway: an archive
		// is the artifact that leaves this machine, and an absolute or
		// escaping name in one is a path-traversal bug in whatever unpacks it.
		if err := safeArchiveName(rel); err != nil {
			return err
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("whetstone pack: header for %s: %w", rel, err)
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("whetstone pack: writing header for %s: %w", rel, err)
		}

		src, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("whetstone pack: %w", err)
		}
		defer func() { _ = src.Close() }()

		if _, err := io.Copy(tw, src); err != nil {
			return fmt.Errorf("whetstone pack: copying %s: %w", rel, err)
		}
		return nil
	})
}

// safeArchiveName rejects an entry name that is absolute or contains a
// traversal segment.
func safeArchiveName(name string) error {
	if name == "" || filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return fmt.Errorf("whetstone pack: refusing absolute archive entry %q", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return fmt.Errorf("whetstone pack: refusing archive entry %q, which escapes the bundle", name)
		}
	}
	return nil
}

func init() {
	packCmd.Flags().StringVarP(&packOutput, "output", "o", "", "Archive path (default <skill>.tar.gz)")
	rootCmd.AddCommand(packCmd)
}
