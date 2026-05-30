package platform

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorage_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocalStorage(dir)
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}

	content := "hello, skael"
	path, err := s.Write("archives/test.tar.gz", strings.NewReader(content))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	rc, err := s.Read("archives/test.tar.gz")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if string(got) != content {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}

	// Write returns the stored key (the name), not a filesystem path.
	if path != "archives/test.tar.gz" {
		t.Errorf("Write returned key %q, want %q", path, "archives/test.tar.gz")
	}
}

func TestStorage_Delete(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocalStorage(dir)
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}

	_, err = s.Write("to-delete.tar.gz", strings.NewReader("data"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := s.Delete("to-delete.tar.gz"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = s.Read("to-delete.tar.gz")
	if err == nil {
		t.Fatal("Read after Delete: expected error, got nil")
	}
	if !os.IsNotExist(err) {
		t.Errorf("Read after Delete: expected not-found error, got %v", err)
	}
}

func TestStorage_WriteAtomic(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocalStorage(dir)
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}

	_, err = s.Write("atomic.tar.gz", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// No .tmp files should remain after a successful write
	var tmpFiles []string
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".tmp") {
			tmpFiles = append(tmpFiles, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(tmpFiles) != 0 {
		t.Errorf("found leftover .tmp files after Write: %v", tmpFiles)
	}
}

func TestStorage_PathTraversal_Rejected(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocalStorage(dir)
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}

	_, err = s.Write("../../etc/evil.tar.gz", strings.NewReader("evil"))
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected error to contain 'traversal', got: %v", err)
	}
}

func TestStorage_PathTraversal_NestedEscape(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocalStorage(dir)
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}

	_, err = s.Write("skills/../../../etc/passwd", strings.NewReader("evil"))
	if err == nil {
		t.Fatal("expected error for nested path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected error to contain 'traversal', got: %v", err)
	}
}
