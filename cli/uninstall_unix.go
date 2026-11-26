//go:build !windows

package cli

import "errors"

// removeUserPathEntry is Windows only. install.sh adds no PATH entry.
func removeUserPathEntry(string) error { return nil }

// deleteSelf is reached only for an install.ps1 install, which is Windows only.
func deleteSelf(string) error { return errors.New("self-delete is Windows only") }
