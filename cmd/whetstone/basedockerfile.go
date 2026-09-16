package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
)

// The release workflow builds the published image from this, so the image a
// Kubernetes worker pulls and the one a Docker worker builds are the same bytes.
func printBaseDockerfile(slim bool) string { return imagespec.BaseDockerfile(slim) }

// The version suffix of imagespec.DefaultBaseTag. Derived in Go rather than
// parsed out of the source by the release workflow, so a change to the constant
// either compiles or fails — it never desyncs the tag published under.
func printBaseTag() string {
	_, suffix, _ := strings.Cut(imagespec.DefaultBaseTag, ":")
	return suffix
}

// Checked before cobra parses anything. Both flags are hidden because they exist
// for the release workflow's "images" job, not for a user.
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
