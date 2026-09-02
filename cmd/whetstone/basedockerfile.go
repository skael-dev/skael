package main

import (
	"fmt"
	"os"

	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
)

// printBaseDockerfile emits the embedded base image definition. The release
// workflow builds the published image from this, so the image a Kubernetes
// worker pulls and the image a Docker worker builds are the same bytes.
func printBaseDockerfile(slim bool) string { return imagespec.BaseDockerfile(slim) }

// handlePrintBaseDockerfile checks argv for the hidden --print-base-dockerfile
// flag before cobra parses anything, and exits the process when found. It is
// hidden because it exists for the release workflow, not for a user: see
// task 10's CI job and the "images" job in .github/workflows/release.yml.
func handlePrintBaseDockerfile(args []string) bool {
	print := false
	slim := false
	for _, a := range args {
		switch a {
		case "--print-base-dockerfile":
			print = true
		case "--slim":
			slim = true
		}
	}
	if !print {
		return false
	}
	fmt.Print(printBaseDockerfile(slim))
	os.Exit(0)
	return true
}
