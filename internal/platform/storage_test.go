package platform

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/require"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
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

// runStorageConformance exercises the Storage contract: write → read → delete.
func runStorageConformance(t *testing.T, s Storage) {
	t.Helper()
	content := []byte("hello-archive")
	key := "skill-x/abc123.tar.gz"

	got, err := s.Write(key, bytes.NewReader(content))
	require.NoError(t, err)
	require.Equal(t, key, got)

	rc, err := s.Read(key)
	require.NoError(t, err)
	data, err := io.ReadAll(rc)
	rc.Close()
	require.NoError(t, err)
	require.Equal(t, content, data)

	require.NoError(t, s.Delete(key))
}

func TestLocalStorage_Conformance(t *testing.T) {
	s, err := NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	runStorageConformance(t, s)
}

const minioImage = "minio/minio:RELEASE.2024-01-16T16-07-38Z"

func startMinio(t *testing.T) (endpoint, user, pass string) {
	t.Helper()
	ctx := context.Background()
	mc, err := tcminio.Run(ctx, minioImage)
	require.NoError(t, err)
	t.Cleanup(func() { _ = mc.Terminate(ctx) })
	ep, err := mc.ConnectionString(ctx)
	require.NoError(t, err)
	return ep, mc.Username, mc.Password
}

func TestS3Storage_Conformance(t *testing.T) {
	endpoint, user, pass := startMinio(t)
	t.Setenv("S3_ENDPOINT", endpoint)
	t.Setenv("S3_ACCESS_KEY_ID", user)
	t.Setenv("S3_SECRET_ACCESS_KEY", pass)
	t.Setenv("S3_USE_SSL", "false")
	t.Setenv("S3_USE_PATH_STYLE", "true")

	// Create the bucket the storage expects.
	client, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(user, pass, ""),
		Secure:       false,
		BucketLookup: minio.BucketLookupPath,
	})
	require.NoError(t, err)
	require.NoError(t, client.MakeBucket(context.Background(), "skael-test", minio.MakeBucketOptions{}))

	s, err := newS3Storage("s3://skael-test/archives")
	require.NoError(t, err)
	runStorageConformance(t, s)
}

func TestS3Storage_BucketMissing(t *testing.T) {
	endpoint, user, pass := startMinio(t)
	t.Setenv("S3_ENDPOINT", endpoint)
	t.Setenv("S3_ACCESS_KEY_ID", user)
	t.Setenv("S3_SECRET_ACCESS_KEY", pass)
	t.Setenv("S3_USE_SSL", "false")
	t.Setenv("S3_USE_PATH_STYLE", "true")

	_, err := newS3Storage("s3://no-such-bucket/x")
	require.Error(t, err)
}
