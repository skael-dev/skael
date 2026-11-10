package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var addCmd = &cobra.Command{
	Use:   "add <skill | pattern> | --set <name>",
	Short: "Follow a skill, a pattern or a set, and install it",
	Long: `Follow a skill, every released skill under a pattern, or a set, and install
what it resolves to. "skael sync" and the auto-sync hook keep everything you
follow current, so a new skill released under a followed pattern or added to a
followed set arrives with no command.

Skills go to .agents/skills, which Codex, Cursor and OpenCode read, and also
to .claude/skills when Claude Code is detected.

--scope decides where:
  project  <repo>/.agents/skills/<name>, and <repo>/.claude/skills/<name>.
           The repo is the nearest ancestor with .git, or the current
           directory. This is the default.
  user     ~/.agents/skills/<name>, and ~/.claude/skills/<name>, available in
           every project.

The default comes from "skael setup --scope". When two followed sources want
different versions of one skill, add refuses and names both.`,
	Example: `  skael add code-review
  skael add 'payments:*'
  skael add --set payments
  skael add code-review --scope user`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAdd,
}

var (
	addScope string
	addSet   string
)

func init() {
	addCmd.Flags().StringVar(&addScope, "scope", "", "Skill placement scope: project|user (overrides default)")
	addCmd.Flags().StringVar(&addSet, "set", "", "Follow a set by name")
	rootCmd.AddCommand(addCmd)
}

func runAdd(cmd *cobra.Command, args []string) error {
	if addScope != "" && !validScope(addScope) {
		return usageError("invalid_scope", fmt.Sprintf("invalid --scope %q: must be \"project\" or \"user\"", addScope), "")
	}
	arg := ""
	if len(args) == 1 {
		arg = args[0]
	}
	src, err := sourceOf(arg, addSet, addScope)
	if err != nil {
		return usageError("invalid_source", err.Error(), "skael add --help")
	}

	dir := config.DefaultDir()
	cfg, _, err := config.EnsureSkillsKey(dir)
	if err != nil {
		return notConfigured(err)
	}
	label := sourceLabel(src)
	if existing, idx := cfg.FindSource(src.Kind, src.Name); idx >= 0 && (src.Scope == "" || existing.Scope == src.Scope) {
		if ui.JSONMode {
			return ui.PrintJSON(map[string]string{"status": "already_following", "source": label})
		}
		ui.Info("Already following %s.", label)
		return nil
	} else if idx >= 0 {
		cfg.Skills = append(cfg.Skills[:idx], cfg.Skills[idx+1:]...)
	}

	c := client.New(cfg.Endpoint, cfg.APIKey)
	sp := StartSpinner("Checking registry...")
	if err := checkSource(c, src); err != nil {
		sp.Stop()
		return err
	}

	// Refuse before anything is recorded when the new source asks for a
	// version another followed source already pins differently.
	cfg.Skills = append(cfg.Skills, src)
	exp, err := expand(c, cfg, "")
	if err != nil {
		sp.Stop()
		return apiFailure(err)
	}
	var conflicts []string
	for _, cf := range exp.Conflicts {
		if strings.Contains(cf, label) {
			conflicts = append(conflicts, cf)
		}
	}
	if len(conflicts) > 0 {
		sp.Stop()
		return &exitError{exit: exitUsage, code: "version_conflict",
			msg:     fmt.Sprintf("not following %s: another followed source wants a different version", label),
			context: strings.Join(conflicts, "\n    ")}
	}
	if err := config.WriteConfig(dir, cfg); err != nil {
		sp.Stop()
		return failure("config_error", fmt.Sprintf("write config: %s", err), "")
	}
	sp.Update("Installing...")
	res, err := reconcile(c, cfg, dir, reconcileOptions{Quiet: ui.JSONMode})
	sp.Stop()
	if err != nil {
		return apiFailure(err)
	}
	matched := 0
	for _, w := range exp.Skills {
		if slices.Contains(w.Sources, label) {
			matched++
		}
	}
	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{"status": "following", "source": label, "skills": matched, "synced": res.Synced, "failed": res.Failed})
	}
	if src.Kind == sourcePattern && matched == 0 {
		ui.Info("Following %s. No released skill matches it yet; sync installs one when it does.", label)
	} else {
		ui.Success("Following %s: %d %s (%s scope)", label, matched, plural(matched, "skill", "skills"), resolveScope(src.Scope, cfg.Scope))
	}
	if res.Failed > 0 {
		return syncFailed(res.Failed, nil)
	}
	return nil
}

// checkSource refuses a skill or a set the registry does not have. A pattern
// needs no check: it may match skills released later.
func checkSource(c *client.Client, src config.SkillEntry) error {
	switch src.Kind {
	case sourcePattern:
		return nil
	case sourceSet:
		if _, err := c.ResolveSet(src.Name); err != nil {
			return failure("not_found", fmt.Sprintf("set %q: %s", src.Name, err), "skael sets list")
		}
		return nil
	}
	manifest, err := c.GetManifest()
	if err != nil {
		return apiFailure(fmt.Errorf("fetch manifest: %w", err))
	}
	if !slices.ContainsFunc(manifest, func(m client.ManifestEntry) bool { return m.Name == src.Name }) {
		return failure("not_found", fmt.Sprintf("skill %q not found on registry", src.Name), "skael search <term>")
	}
	return nil
}
