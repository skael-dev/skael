package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
)

// printBaseDockerfile emits the embedded base image definition. The release
// workflow builds the published image from this, so the image a Kubernetes
// worker pulls and the image a Docker worker builds are the same bytes.
func printBaseDockerfile(slim bool) string { return imagespec.BaseDockerfile(slim) }

// printBaseTag returns the version suffix of imagespec.DefaultBaseTag (for
// "whetstone-base:1", it returns "1"). Deriving it from the constant in Go,
// rather than parsing imagespec.go's source text in the release workflow,
// means a change to the constant either keeps working or fails to compile —
// never silently desyncs the tag the workflow publishes under.
func printBaseTag() string {
	_, suffix, _ := strings.Cut(imagespec.DefaultBaseTag, ":")
	return suffix
}

// handlePrintBaseDockerfile checks argv for the hidden --print-base-dockerfile
// and --print-base-tag flags before cobra parses anything, and exits the
// process when either is found. Both are hidden because they exist for the
// release workflow, not for a user: see task 10's CI job and the "images" job
// in .github/workflows/release.yml.
func handlePrintBaseDockerfile(args []string) bool {
	print := false
	printTag := false
	slim := false
	for _, a := range args {
		switch a {
		case "--print-base-dockerfile":
			print = true
		case "--print-base-tag":
			printTag = true
		case "--slim":
			slim = true
		}
	}
	switch {
	case printTag:
		fmt.Print(printBaseTag())
	case print:
		fmt.Print(printBaseDockerfile(slim))
	default:
		return false
	}
	os.Exit(0)
	return true
}
