package whetstone

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/ui"
)

// workspaceDirName is the directory store.Open creates under the project root.
// store owns the name; it is repeated here only so the CLI can find an
// existing workspace by walking up from the working directory, which store
// itself has no reason to do.
const workspaceDirName = ".whetstone"

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a .whetstone workspace in the current directory",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := RunInit("")
		if err != nil {
			return err
		}
		if ui.JSONMode {
			return ui.PrintJSON(map[string]string{"workspace": root})
		}
		ui.Success("workspace ready at %s", root)
		return nil
	},
}

// RunInit creates or opens the workspace under root, defaulting to the working
// directory when root is empty, and returns the workspace directory.
func RunInit(root string) (string, error) {
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("whetstone init: %w", err)
		}
		root = wd
	}

	if found, err := ancestorWorkspace(root); err != nil {
		return "", fmt.Errorf("whetstone init: %w", err)
	} else if found != "" {
		return "", fmt.Errorf("whetstone init: %s is inside the workspace at %s; a nested workspace shadows it for every later command — run from %s, or move this directory out", root, found, found)
	}

	s, err := store.Open(root)
	if err != nil {
		return "", err
	}
	defer func() { _ = s.Close() }()
	return s.Root(), nil
}

// ancestorWorkspace walks up from root's parent looking for an existing
// .whetstone workspace, so RunInit can refuse to create a nested one: every
// later command run from inside root would find the nested workspace first
// and silently shadow the outer one. It returns "" (with a nil error) when no
// ancestor holds one. root itself is not checked — re-running init on an
// existing workspace is the idempotent case store.Open already handles.
func ancestorWorkspace(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(abs)
	for {
		if info, err := os.Stat(filepath.Join(dir, workspaceDirName)); err == nil && info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// findWorkspace walks up from the working directory to the nearest ancestor
// holding a .whetstone directory, so commands work from anywhere inside a
// project the way git does.
func findWorkspace() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, workspaceDirName)); err == nil && info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s workspace found here or in any parent directory; run `whetstone init`", workspaceDirName)
		}
		dir = parent
	}
}

// openStore opens the nearest existing workspace. It deliberately never
// creates one: a mistyped directory silently becoming a fresh empty workspace
// looks exactly like a lost skill.
func openStore() (*store.Store, error) {
	root, err := findWorkspace()
	if err != nil {
		return nil, err
	}
	return store.Open(root)
}

func init() {
	rootCmd.AddCommand(initCmd)
}
