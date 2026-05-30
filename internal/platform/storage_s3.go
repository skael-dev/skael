package platform

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Storage stores archives in any S3-compatible object store.
type S3Storage struct {
	client *minio.Client
	bucket string
	prefix string
}

// envOr returns the first non-empty environment variable among keys.
func envOr(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// newS3Storage parses "s3://bucket/prefix" and configures a client from
// S3_*/AWS_* env vars. Credentials fall back to an IAM instance role when no
// static keys are set. The bucket must already exist.
func newS3Storage(storagePath string) (Storage, error) {
	u, err := url.Parse(storagePath)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("storage: invalid s3 path %q (want s3://bucket/prefix)", storagePath)
	}
	bucket := u.Host
	prefix := strings.Trim(u.Path, "/")

	endpoint := envOr("S3_ENDPOINT")
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}
	region := envOr("S3_REGION", "AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}
	useSSL := os.Getenv("S3_USE_SSL") != "false"

	var creds *credentials.Credentials
	ak := envOr("S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID")
	sk := envOr("S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY")
	if ak != "" && sk != "" {
		creds = credentials.NewStaticV4(ak, sk, "")
	} else {
		creds = credentials.NewIAM("") // EC2/ECS/EKS instance role
	}

	opts := &minio.Options{Creds: creds, Secure: useSSL, Region: region}
	if os.Getenv("S3_USE_PATH_STYLE") == "true" {
		opts.BucketLookup = minio.BucketLookupPath
	}
	client, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("storage: s3 client: %w", err)
	}

	exists, err := client.BucketExists(context.Background(), bucket)
	if err != nil {
		return nil, fmt.Errorf("storage: s3 bucket %q not accessible: %w", bucket, err)
	}
	if !exists {
		return nil, fmt.Errorf("storage: s3 bucket %q does not exist", bucket)
	}
	return &S3Storage{client: client, bucket: bucket, prefix: prefix}, nil
}

func (s *S3Storage) key(name string) (string, error) {
	clean := strings.TrimLeft(name, "/")
	if clean == "" || strings.Contains(clean, "..") {
		return "", fmt.Errorf("storage: invalid object name %q", name)
	}
	return path.Join(s.prefix, clean), nil
}

func (s *S3Storage) Write(name string, r io.Reader) (string, error) {
	key, err := s.key(name)
	if err != nil {
		return "", err
	}
	_, err = s.client.PutObject(context.Background(), s.bucket, key, r, -1,
		minio.PutObjectOptions{ContentType: "application/gzip"})
	if err != nil {
		return "", fmt.Errorf("storage: s3 put %q: %w", name, err)
	}
	return name, nil
}

func (s *S3Storage) Read(name string) (io.ReadCloser, error) {
	key, err := s.key(name)
	if err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(context.Background(), s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage: s3 get %q: %w", name, err)
	}
	return obj, nil
}

func (s *S3Storage) Delete(name string) error {
	key, err := s.key(name)
	if err != nil {
		return err
	}
	return s.client.RemoveObject(context.Background(), s.bucket, key, minio.RemoveObjectOptions{})
}
