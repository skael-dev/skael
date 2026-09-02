// Command whetstone is the standalone skill authoring and evaluation CLI.
package main

import (
	"os"

	"github.com/skael-dev/skael/cli/whetstone"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// Checked ahead of cobra: this flag exists for the release workflow, not
	// for a user, and must not appear in any command's --help output.
	if handlePrintBaseDockerfile(os.Args[1:]) {
		return
	}

	whetstone.SetVersion(version, commit, date)
	whetstone.Execute()
}
