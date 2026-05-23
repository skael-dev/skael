package platform

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Storage provides local filesystem storage for skill archive files.
type Storage struct {
	BasePath string
}

// NewStorage creates a Storage rooted at basePath, creating the directory if
// it does not already exist.
func NewStorage(basePath string) (*Storage, error) {
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return nil, fmt.Errorf("storage: create base path %q: %w", basePath, err)
	}
	return &Storage{BasePath: basePath}, nil
}

// Write stores the content from r under name (relative to BasePath).
// It uses an atomic write: content is first written to a .tmp file which is
// then renamed to the final destination, ensuring no partial files are visible.
// Returns the full path of the written file.
func (s *Storage) Write(name string, r io.Reader) (string, error) {
	dest := filepath.Join(s.BasePath, name)

	// Prevent path traversal: verify the resolved path stays within BasePath.
	absPath, err := filepath.Abs(dest)
	if err != nil {
		return "", fmt.Errorf("storage: resolve path %q: %w", name, err)
	}
	absBase, err := filepath.Abs(s.BasePath)
	if err != nil {
		return "", fmt.Errorf("storage: resolve base path: %w", err)
	}
	if !strings.HasPrefix(absPath, absBase+string(os.PathSeparator)) {
		return "", fmt.Errorf("storage: path traversal detected: %s", name)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("storage: create parent dirs for %q: %w", name, err)
	}

	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("storage: create temp file for %q: %w", name, err)
	}

	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("storage: write %q: %w", name, err)
	}

	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("storage: close temp file for %q: %w", name, err)
	}

	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("storage: rename to final path %q: %w", name, err)
	}

	return dest, nil
}

// Read opens the file stored under name (relative to BasePath) for reading.
// The caller is responsible for closing the returned ReadCloser.
func (s *Storage) Read(name string) (io.ReadCloser, error) {
	path := filepath.Join(s.BasePath, name)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Delete removes the file stored under name (relative to BasePath).
func (s *Storage) Delete(name string) error {
	path := filepath.Join(s.BasePath, name)
	return os.Remove(path)
}
