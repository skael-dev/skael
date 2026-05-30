package platform

import "fmt"

func newS3Storage(storagePath string) (Storage, error) {
	return nil, fmt.Errorf("storage: s3 backend not yet implemented (%s)", storagePath)
}
