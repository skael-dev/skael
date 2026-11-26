package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/skael-dev/skael/cli/agents"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/cli/hooks"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove everything the skael CLI wrote on this machine",
	Long: `Remove the skael hooks and plugins from every agent, the skills that sync
installed, and ~/.skael. A binary that install.ps1 or install.sh installed is
removed too; for Scoop or Homebrew, the command to run is printed.

Hook entries and plugin files that Skael did not write stay. Skills that
"skael init" wrote into a project, and your API key on the server, stay.`,
	Example: `  skael uninstall
  skael uninstall --keep-skills
  skael uninstall --yes`,
	Args: cobra.NoArgs,
	RunE: runUninstall,
}

var (
	uninstallKeepSkills bool
	uninstallYes        bool
)

func init() {
	uninstallCmd.Flags().BoolVar(&uninstallKeepSkills, "keep-skills", false, "Keep the skills that sync installed")
	uninstallCmd.Flags().BoolVarP(&uninstallYes, "yes", "y", false, "Do not ask for confirmation")
	rootCmd.AddCommand(uninstallCmd)
}

// installKind is how the running binary was installed.
type installKind string

const (
	installPS1      installKind = "install.ps1"
	installSh       installKind = "install.sh"
	installScoop    installKind = "scoop"
	installHomebrew installKind = "homebrew"
	installOther    installKind = "other"
)

// detectInstall classifies exe, an absolute path with symlinks resolved.
// localAppData and installDir are %LOCALAPPDATA% and the install dir variable
// the install script honours (SKAEL_INSTALL_DIR or INSTALL_DIR), each empty
// when unset.
func detectInstall(exe, goos, localAppData, installDir string) installKind {
	if goos == "windows" {
		// Compare Windows paths case-insensitively with one separator, so the
		// result does not depend on how a variable spells the path.
		norm := func(p string) string { return path.Clean(strings.ToLower(strings.ReplaceAll(p, `\`, "/"))) }
		e := norm(exe)
		if strings.Contains(e, "/scoop/apps/skael/") {
			return installScoop
		}
		dir := path.Dir(e)
		if installDir != "" && dir == norm(installDir) {
			return installPS1
		}
		if localAppData != "" && dir == norm(localAppData)+"/programs/skael/bin" {
			return installPS1
		}
		return installOther
	}
	if strings.Contains(exe, "/Caskroom/skael/") || strings.Contains(exe, "/Cellar/skael/") {
		return installHomebrew
	}
	// Unix paths always use "/", whatever OS runs this.
	dir := path.Dir(exe)
	if installDir != "" && dir == path.Clean(installDir) {
		return installSh
	}
	if dir == "/usr/local/bin" {
		return installSh
	}
	return installOther
}

// uninstallPlan is everything uninstall removes, listed before anything goes.
type uninstallPlan struct {
	Hooks     []string    `json:"hooks"`
	Skills    []string    `json:"skills"`
	ConfigDir string      `json:"config_dir,omitempty"`
	Binary    string      `json:"binary"`
	Install   installKind `json:"install"`
}

func runUninstall(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return failure("home_error", fmt.Sprintf("cannot determine home directory: %s", err), "")
	}
	dir := config.DefaultDir()
	plan, err := planUninstall(home, dir)
	if err != nil {
		return failure("uninstall_error", err.Error(), "")
	}

	if !uninstallYes {
		if ui.JSONMode || !term.IsTerminal(int(os.Stdin.Fd())) {
			return usageError("confirmation_required", "uninstall deletes files and needs confirmation", "skael uninstall --yes")
		}
		printPlan(plan)
		fmt.Fprint(os.Stderr, "\n  Remove these? [y/N] ")
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			ui.Info("Nothing removed.")
			return nil
		}
	}

	failed := 0
	for _, agent := range agents.Known() {
		path := agent.ConfigPath(home)
		if err := hooks.UninstallForAgent(agent.Name(), path); err != nil {
			ui.Errorf("remove the %s hook: %s", agent.Name(), err)
			failed++
		}
		if err := hooks.UninstallAutoSyncForAgent(agent.Name(), path); err != nil {
			ui.Errorf("remove the %s auto-sync hook: %s", agent.Name(), err)
			failed++
		}
	}
	if len(plan.Hooks) > 0 {
		ui.Success("Removed the hooks from %d %s", len(plan.Hooks), plural(len(plan.Hooks), "agent", "agents"))
	}

	for _, p := range plan.Skills {
		if err := os.RemoveAll(p); err != nil {
			ui.Errorf("remove %s: %s", p, err)
			failed++
		}
	}
	if len(plan.Skills) > 0 {
		ui.Success("Removed %d synced skill %s", len(plan.Skills), plural(len(plan.Skills), "folder", "folders"))
	}

	if plan.ConfigDir != "" {
		if err := os.RemoveAll(plan.ConfigDir); err != nil {
			ui.Errorf("remove %s: %s", plan.ConfigDir, err)
			failed++
		} else {
			ui.Success("Removed %s", plan.ConfigDir)
		}
	}

	next, err := removeBinary(plan)
	if err != nil {
		ui.Errorf("remove %s: %s", plan.Binary, err)
		failed++
	}

	if ui.JSONMode {
		out := map[string]any{"status": "uninstalled", "removed": plan}
		if next != "" {
			out["next"] = next
		}
		if failed > 0 {
			out["status"] = "partial"
		}
		if err := ui.PrintJSON(out); err != nil {
			return err
		}
	} else {
		if next != "" {
			ui.Info("To remove the binary, run: %s", next)
		}
		ui.Info("Your API key stays valid on the server. Delete it in the web app under Settings → API & Keys.")
	}
	if failed > 0 {
		return failure("uninstall_error", fmt.Sprintf("%d %s failed", failed, plural(failed, "step", "steps")), "")
	}
	return nil
}

// planUninstall reads what is on disk now. It names only hooks that carry a
// skael marker and skill folders that state.json records.
func planUninstall(home, dir string) (uninstallPlan, error) {
	plan := uninstallPlan{Hooks: []string{}, Skills: []string{}}
	for _, agent := range agents.Known() {
		st := hooks.State(agent.Name(), agent.ConfigPath(home), "")
		if st.Activation || st.AutoSync {
			plan.Hooks = append(plan.Hooks, fmt.Sprintf("%s (%s)", agent.Name(), agent.ConfigPath(home)))
		}
	}
	if !uninstallKeepSkills {
		state, err := config.ReadState(dir)
		if err != nil {
			return plan, fmt.Errorf("read state: %w", err)
		}
		for _, s := range state.Skills {
			for _, p := range s.Placements {
				// A placement always ends in the skill's own folder. Anything
				// else in a hand-edited state file is not ours to delete.
				if !filepath.IsAbs(p.Path) || filepath.Base(p.Path) != config.SkillFolder(s.Name) {
					continue
				}
				if _, err := os.Stat(p.Path); err == nil {
					plan.Skills = append(plan.Skills, p.Path)
				}
			}
		}
	}
	if _, err := os.Stat(dir); err == nil {
		plan.ConfigDir = dir
	}
	exe, err := os.Executable()
	if err != nil {
		return plan, fmt.Errorf("find the skael binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	plan.Binary = exe
	installDir := os.Getenv("INSTALL_DIR")
	if runtime.GOOS == "windows" {
		installDir = os.Getenv("SKAEL_INSTALL_DIR")
	}
	if resolved, err := filepath.EvalSymlinks(installDir); installDir != "" && err == nil {
		installDir = resolved
	}
	plan.Install = detectInstall(exe, runtime.GOOS, os.Getenv("LOCALAPPDATA"), installDir)
	return plan, nil
}

func printPlan(plan uninstallPlan) {
	fmt.Fprintln(os.Stderr, "\n  skael uninstall removes:")
	for _, h := range plan.Hooks {
		fmt.Fprintf(os.Stderr, "    hooks   %s\n", h)
	}
	for _, s := range plan.Skills {
		fmt.Fprintf(os.Stderr, "    skill   %s\n", s)
	}
	if plan.ConfigDir != "" {
		fmt.Fprintf(os.Stderr, "    config  %s\n", plan.ConfigDir)
	}
	switch plan.Install {
	case installPS1:
		fmt.Fprintf(os.Stderr, "    binary  %s and its PATH entry\n", plan.Binary)
	case installSh:
		fmt.Fprintf(os.Stderr, "    binary  %s\n", plan.Binary)
	default:
		fmt.Fprintf(os.Stderr, "    binary  stays at %s; remove it with: %s\n", plan.Binary, manualRemove(plan))
	}
	if len(plan.Hooks) == 0 && len(plan.Skills) == 0 && plan.ConfigDir == "" {
		fmt.Fprintln(os.Stderr, "    no hooks, skills or config found")
	}
}

// removeBinary removes a binary an install script put in place, and returns
// the command the user runs for any other install.
func removeBinary(plan uninstallPlan) (next string, err error) {
	switch plan.Install {
	case installPS1:
		if err := removeUserPathEntry(filepath.Dir(plan.Binary)); err != nil {
			ui.Warn("remove %s from your user PATH: %s", filepath.Dir(plan.Binary), err)
		}
		if err := deleteSelf(plan.Binary); err != nil {
			return "", err
		}
		ui.Success("Removing %s once this command exits", plan.Binary)
		return "", nil
	case installSh:
		err := os.Remove(plan.Binary)
		if errors.Is(err, fs.ErrPermission) {
			return "sudo rm " + plan.Binary, nil
		}
		if err != nil {
			return "", err
		}
		ui.Success("Removed %s", plan.Binary)
		return "", nil
	}
	return manualRemove(plan), nil
}

// manualRemove is the command that removes a binary uninstall leaves alone.
func manualRemove(plan uninstallPlan) string {
	switch plan.Install {
	case installScoop:
		return "scoop uninstall skael"
	case installHomebrew:
		return "brew uninstall --cask skael"
	}
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`del "%s"`, plan.Binary)
	}
	return "rm " + plan.Binary
}
