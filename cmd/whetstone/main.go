// Command whetstone is the standalone skill authoring and evaluation CLI.
package main

import "github.com/skael-dev/skael/cli/whetstone"

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	whetstone.SetVersion(version, commit, date)
	whetstone.Execute()
}
