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

// maxWorkspaceFileSize and maxWorkspaceTotalSize bound one session's mirror in
// each direction. A bind mount, which the Docker driver uses instead, costs
// the worker process nothing; this path copies the workspace through the
// worker's own memory, so an unbounded session output would exhaust it and
// take every concurrent session down with it. The limits are generous
// compared to internal/skill's archive limits, because a sandboxed agent run
// can legitimately produce build artifacts far larger than a skill bundle.
// var, not const, so a test can shrink them rather than writing gigabytes of
// fixture data to exercise the cap.
var (
	maxWorkspaceFileSize  int64 = 200 << 20  // 200 MiB
	maxWorkspaceTotalSize int64 = 2000 << 20 // ~2 GiB
)

// stageIn mirrors the local workspace into the pod. The pod's tar reads the
// stream on stdin, which is the same shape the Docker driver's "docker cp -"
// uses. tarDir runs in its own goroutine and writes into an io.Pipe so the
// workspace is streamed to the exec call rather than buffered whole in RAM.
func (d *Driver) stageIn(ctx context.Context, pod, workdir, local string) error {
	pr, pw := io.Pipe()
	tarErrCh := make(chan error, 1)
	go func() {
		err := tarDir(local, pw)
		tarErrCh <- err
		pw.CloseWithError(err)
	}()

	var stderr bytes.Buffer
	code, err := d.ex.Exec(ctx, execRequest{
		Pod: pod, Container: "session",
		Argv:   []string{"tar", "-xf", "-", "-C", workdir},
		Stdin:  pr,
		Stderr: &stderr,
	})
	// Exec can return without having read all of pr, or any of it, when it
	// fails before consuming stdin. Closing the read side turns the tarDir
	// goroutine's pending Write into ErrClosedPipe instead of leaving it
	// blocked forever.
	pr.Close()
	tarErr := <-tarErrCh

	// An exec failure is checked first: when exec aborts early, tarErr is
	// usually just the closed-pipe side effect above, not the reason
	// staging failed.
	if err != nil {
		return fmt.Errorf("kubernetes: staging the workspace into %s: %w\n%s", pod, err, stderr.String())
	}
	if code != 0 {
		return fmt.Errorf("kubernetes: staging the workspace into %s: tar exited %d\n%s", pod, code, stderr.String())
	}
	if tarErr != nil {
		return fmt.Errorf("kubernetes: packing the workspace: %w", tarErr)
	}
	return nil
}

type execResult struct {
	code int
	err  error
}

// collectOut mirrors the pod's workspace back onto the caller's disk. Any
// failure is an error: a partial mirror is indistinguishable from a skill that
// produced nothing, and would be graded as one. The exec's stdout is streamed
// straight into untarInto through an io.Pipe, so a large workspace never
// lands whole in the worker's memory.
func (d *Driver) collectOut(ctx context.Context, pod, workdir, local string) error {
	pr, pw := io.Pipe()
	var stderr bytes.Buffer
	resCh := make(chan execResult, 1)
	go func() {
		code, err := d.ex.Exec(ctx, execRequest{
			Pod: pod, Container: "session",
			Argv:   []string{"tar", "-cf", "-", "-C", workdir, "."},
			Stdout: pw,
			Stderr: &stderr,
		})
		pw.CloseWithError(err)
		resCh <- execResult{code: code, err: err}
	}()

	untarErr := untarInto(local, pr)
	res := <-resCh
	if res.err != nil {
		return fmt.Errorf("kubernetes: collecting the workspace from %s: %w\n%s", pod, res.err, stderr.String())
	}
	if res.code != 0 {
		return fmt.Errorf("kubernetes: collecting the workspace from %s: tar exited %d\n%s", pod, res.code, stderr.String())
	}
	if untarErr != nil {
		return fmt.Errorf("kubernetes: unpacking the workspace from %s: %w", pod, untarErr)
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
	var totalSize int64
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
			// Reject an oversized file by its declared size before writing
			// anything, the same per-file budget internal/skill.Unpack
			// enforces on the way in.
			if h.Size > maxWorkspaceFileSize {
				return fmt.Errorf("kubernetes: workspace file %q exceeds the %d byte per-file limit (%d bytes)", h.Name, maxWorkspaceFileSize, h.Size)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fs.FileMode(h.Mode)&0o777)
			if err != nil {
				return err
			}
			// +1 so a stream that declares an honest size but writes past it
			// is caught, rather than silently truncated.
			n, err := io.Copy(f, io.LimitReader(tr, maxWorkspaceFileSize+1))
			if err != nil {
				f.Close()
				return err
			}
			if n > maxWorkspaceFileSize {
				f.Close()
				return fmt.Errorf("kubernetes: workspace file %q exceeds the %d byte per-file limit", h.Name, maxWorkspaceFileSize)
			}
			if err := f.Close(); err != nil {
				return err
			}
			totalSize += n
			if totalSize > maxWorkspaceTotalSize {
				return fmt.Errorf("kubernetes: workspace exceeds the %d byte total extraction limit", maxWorkspaceTotalSize)
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
