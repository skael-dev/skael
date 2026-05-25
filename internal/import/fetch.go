package skillimport

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxTarballSize = 50 << 20 // 50 MB
	fetchTimeout   = 30 * time.Second
)

type Fetcher struct {
	apiBase     string
	githubToken string
	httpClient  *http.Client
}

type FetchResult struct {
	Dir       string // temp directory with unpacked contents (caller must clean up)
	CommitSHA string // extracted from tarball root dir name
}

func NewFetcher(apiBase, githubToken string) *Fetcher {
	return &Fetcher{
		apiBase:     apiBase,
		githubToken: githubToken,
		httpClient:  &http.Client{Timeout: fetchTimeout},
	}
}

func (f *Fetcher) Fetch(src Source) (*FetchResult, error) {
	ref := src.Ref
	if ref == "" {
		ref = "HEAD"
	}
	if src.CommitSHA != "" {
		ref = src.CommitSHA
	}

	url := fmt.Sprintf("%s/repos/%s/%s/tarball/%s", f.apiBase, src.Owner, src.Repo, ref)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build fetch request: %w", err)
	}
	if f.githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+f.githubToken)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch tarball: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	limited := io.LimitReader(resp.Body, maxTarballSize+1)

	tmpDir, err := os.MkdirTemp("", "skael-import-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	commitSHA, err := unpackTarball(limited, tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("unpack tarball: %w", err)
	}

	return &FetchResult{Dir: tmpDir, CommitSHA: commitSHA}, nil
}

func unpackTarball(r io.Reader, destDir string) (string, error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return "", fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	var commitSHA string
	var totalSize int64
	var prefix string

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("tar next: %w", err)
		}

		// GitHub tarballs have a root dir like "owner-repo-shortsha/"
		if prefix == "" {
			parts := strings.SplitN(hdr.Name, "/", 2)
			prefix = parts[0] + "/"
			dashParts := strings.Split(parts[0], "-")
			if len(dashParts) >= 3 {
				commitSHA = dashParts[len(dashParts)-1]
			}
		}

		relPath := strings.TrimPrefix(hdr.Name, prefix)
		if relPath == "" {
			continue
		}

		target := filepath.Join(destDir, filepath.FromSlash(relPath))

		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return "", fmt.Errorf("path traversal: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return "", fmt.Errorf("mkdir %s: %w", relPath, err)
			}
		case tar.TypeReg:
			if hdr.Size > 1<<20 {
				return "", fmt.Errorf("file %s exceeds 1 MiB limit", relPath)
			}
			totalSize += hdr.Size
			if totalSize > maxTarballSize {
				return "", fmt.Errorf("total extraction exceeds %d bytes", maxTarballSize)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return "", fmt.Errorf("mkdir for %s: %w", relPath, err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, os.FileMode(hdr.Mode)&0777)
			if err != nil {
				return "", fmt.Errorf("create %s: %w", relPath, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return "", fmt.Errorf("write %s: %w", relPath, err)
			}
			f.Close()
		case tar.TypeSymlink, tar.TypeLink:
			return "", fmt.Errorf("rejected %s: symlinks/hardlinks not allowed", relPath)
		default:
			continue
		}
	}

	return commitSHA, nil
}
