package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Binary returns the skael path that hooks name, and whether it is the skael
// on PATH. The PATH entry wins when it is the running file, because a brew
// symlink or a Scoop shim keeps its path across an upgrade.
func Binary() (path string, onPath bool, err error) {
	exe, err := os.Executable()
	if err != nil {
		return "", false, err
	}
	found, err := exec.LookPath("skael")
	if err != nil {
		return exe, false, nil
	}
	found, err = filepath.Abs(found)
	if err != nil {
		return exe, false, nil
	}
	a, errA := os.Stat(found)
	b, errB := os.Stat(exe)
	if errA == nil && errB == nil && os.SameFile(a, b) {
		return found, true, nil
	}
	return exe, false, nil
}

func reportArgs(agent string) []string { return []string{"hook", "report", "--agent", agent} }

var autoSyncArgs = []string{"hook", "autosync"}

// posixCommand is a command line for sh -c.
func posixCommand(bin string, args []string) string {
	return posixQuote(bin) + " " + strings.Join(args, " ")
}

func posixQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// windowsCommand is a command line for PowerShell. A path with no space or
// shell character stays bare, so the same line also runs under cmd /C.
func windowsCommand(bin string, args []string) string {
	if !strings.ContainsAny(bin, " '\"&;()$`%^|<>{}[]@#,") {
		return bin + " " + strings.Join(args, " ")
	}
	return "& '" + strings.ReplaceAll(bin, "'", "''") + "' " + strings.Join(args, " ")
}

// shellCommand is the command line for the shell an agent runs hooks in on
// this OS.
func shellCommand(bin string, args []string) string {
	if runtime.GOOS == "windows" {
		return windowsCommand(bin, args)
	}
	return posixCommand(bin, args)
}
