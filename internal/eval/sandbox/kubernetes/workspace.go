package kubernetes

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// stageIn mirrors the local workspace into the pod. The pod's tar reads the
// stream on stdin, which is the same shape the Docker driver's "docker cp -"
// uses.
func (d *Driver) stageIn(ctx context.Context, pod, workdir, local string) error {
	var buf bytes.Buffer
	if err := tarDir(local, &buf); err != nil {
		return fmt.Errorf("kubernetes: packing the workspace: %w", err)
	}
	var stderr bytes.Buffer
	code, err := d.ex.Exec(ctx, execRequest{
		Pod: pod, Container: "session",
		Argv:   []string{"tar", "-xf", "-", "-C", workdir},
		Stdin:  &buf,
		Stderr: &stderr,
	})
	if err != nil {
		return fmt.Errorf("kubernetes: staging the workspace into %s: %w\n%s", pod, err, stderr.String())
	}
	if code != 0 {
		return fmt.Errorf("kubernetes: staging the workspace into %s: tar exited %d\n%s", pod, code, stderr.String())
	}
	return nil
}

// collectOut mirrors the pod's workspace back onto the caller's disk. Any
// failure is an error: a partial mirror is indistinguishable from a skill that
// produced nothing, and would be graded as one.
func (d *Driver) collectOut(ctx context.Context, pod, workdir, local string) error {
	var out, stderr bytes.Buffer
	code, err := d.ex.Exec(ctx, execRequest{
		Pod: pod, Container: "session",
		Argv:   []string{"tar", "-cf", "-", "-C", workdir, "."},
		Stdout: &out,
		Stderr: &stderr,
	})
	if err != nil {
		return fmt.Errorf("kubernetes: collecting the workspace from %s: %w\n%s", pod, err, stderr.String())
	}
	if code != 0 {
		return fmt.Errorf("kubernetes: collecting the workspace from %s: tar exited %d\n%s", pod, code, stderr.String())
	}
	if err := untarInto(local, &out); err != nil {
		return fmt.Errorf("kubernetes: unpacking the workspace from %s: %w", pod, err)
	}
	return nil
}

func tarDir(local string, w io.Writer) error {
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(local, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(local, path)
		if err != nil || rel == "." {
			return err
		}
		info, err := de.Info()
		if err != nil {
			return err
		}
		// Regular files and directories only, matching what Unpack accepts
		// elsewhere: a symlink in a workspace is not content to carry.
		if !info.Mode().IsRegular() && !info.IsDir() {
			return nil
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

func untarInto(local string, r io.Reader) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		dest, err := securePath(local, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fs.FileMode(h.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
		// Every other entry type is skipped, including symlinks: the stream
		// comes from inside the sandbox and lands on the caller's disk.
	}
}

// securePath keeps an entry inside dest. The tar stream is produced inside the
// sandbox, so its names are untrusted input on the way out.
func securePath(dest, name string) (string, error) {
	clean := filepath.Clean(filepath.Join(dest, filepath.FromSlash(name)))
	if clean != dest && !strings.HasPrefix(clean, dest+string(os.PathSeparator)) {
		return "", fmt.Errorf("kubernetes: tar entry %q escapes the workspace", name)
	}
	return clean, nil
}
