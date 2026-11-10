package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/internal/pattern"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var setsCmd = &cobra.Command{
	Use:   "sets",
	Short: "Read and manage named sets of skills",
}

var setsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every set",
	Args:  cobra.NoArgs,
	RunE:  runSetsList,
}

var setsGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show a set, its entries and its last callers",
	Args:  cobra.ExactArgs(1),
	RunE:  runSetsGet,
}

var setsResolveCmd = &cobra.Command{
	Use:   "resolve <name>",
	Short: "Resolve a set to exact versions and digests",
	Long: `Resolve a set to exact versions and digests, and list any excluded entries.

An incomplete set is a valid answer here, so this exits 0. Read "complete" in
the output, or use skael pull, which exits 10.`,
	Args: cobra.ExactArgs(1),
	RunE: runSetsResolve,
}

var setsCreateCmd = &cobra.Command{
	Use:   "create <name> [entry...]",
	Short: "Create a set (owner or admin)",
	Long: `Create a set. Write each entry as name, name@latest or name@3, a pattern such
as 'payments:*' or '*' for every released skill, or an exclusion such as
'!payments:legacy', which applies after every include. Quote a pattern so the
shell does not expand it.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runSetsCreate,
}

var setsUpdateCmd = &cobra.Command{
	Use:   "update <name> [entry...]",
	Short: "Change a set's description or replace its entries (owner or admin)",
	Long: `Change a set. Entries you list replace all current entries. With no entries
the current ones stay. With no --description the current one stays.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runSetsUpdate,
}

var setsDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a set (owner or admin)",
	Args:  cobra.ExactArgs(1),
	RunE:  runSetsDelete,
}

var (
	setsCreateDescription string
	setsUpdateDescription string
)

func init() {
	setsCreateCmd.Flags().StringVar(&setsCreateDescription, "description", "", "What the set is for")
	setsUpdateCmd.Flags().StringVar(&setsUpdateDescription, "description", "", "New description")
	setsCmd.AddCommand(setsListCmd, setsGetCmd, setsResolveCmd, setsCreateCmd, setsUpdateCmd, setsDeleteCmd)
	rootCmd.AddCommand(setsCmd)
}

// printRaw prints an API body unchanged apart from indentation.
func printRaw(raw json.RawMessage) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return err
	}
	buf.WriteByte('\n')
	_, err := buf.WriteTo(os.Stdout)
	return err
}

// parseSetEntries reads name, name@latest and name@3, a pattern such as
// payments:* or *, and an exclusion such as !payments:legacy.
func parseSetEntries(args []string) ([]client.SetEntry, error) {
	entries := make([]client.SetEntry, 0, len(args))
	for _, arg := range args {
		if body := strings.TrimPrefix(arg, "!"); body != arg || strings.Contains(arg, "*") {
			if err := pattern.Validate(body); err != nil {
				return nil, usageError("invalid_entry", fmt.Sprintf("entry %q: %s", arg, err), "write payments:*, * or !name")
			}
			entries = append(entries, client.SetEntry{Name: arg})
			continue
		}
		name, version, pinned := strings.Cut(arg, "@")
		if !skillNameRe.MatchString(name) {
			return nil, usageError("invalid_entry", fmt.Sprintf("invalid skill name in entry %q", arg), "write name, name@latest or name@3")
		}
		entry := client.SetEntry{Name: name}
		if pinned && version != "latest" {
			v, err := strconv.Atoi(version)
			if err != nil || v < 1 {
				return nil, usageError("invalid_entry", fmt.Sprintf("invalid version in entry %q", arg), "write name, name@latest or name@3")
			}
			entry.Version = v
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func entryLabel(e client.SetEntry) string {
	if strings.HasPrefix(e.Name, "!") || strings.Contains(e.Name, "*") {
		return e.Name
	}
	if e.Version == 0 {
		return e.Name + "@latest"
	}
	return fmt.Sprintf("%s@%d", e.Name, e.Version)
}

func runSetsList(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	res, err := c.ListSets()
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	if len(res.Value.Sets) == 0 {
		fmt.Fprintln(os.Stdout, "  No sets yet.")
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "  Try: skael sets create <name> <skill>")
		return nil
	}
	for _, s := range res.Value.Sets {
		n := len(s.Entries)
		fmt.Fprintf(os.Stdout, "  %-24s %d %-7s %s\n", s.Name, n, plural(n, "skill", "skills"), truncate(s.Description, 40))
	}
	return nil
}

func runSetsGet(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	res, err := c.GetSet(args[0])
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	set := res.Value
	fmt.Fprintf(os.Stdout, "  %s\n", ui.Bold(set.Name))
	if set.Description != "" {
		fmt.Fprintf(os.Stdout, "    %s\n", set.Description)
	}
	fmt.Fprintln(os.Stdout, "\n  Entries:")
	for _, e := range set.Entries {
		fmt.Fprintf(os.Stdout, "    %s\n", entryLabel(e))
	}
	if len(set.Callers) > 0 {
		fmt.Fprintln(os.Stdout, "\n  Callers:")
		for _, caller := range set.Callers {
			fmt.Fprintf(os.Stdout, "    %-24s %s\n", caller.Name, formatAge(caller.LastSeenAt))
		}
	}
	return nil
}

func runSetsResolve(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	res, err := c.ResolveSet(args[0])
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	r := res.Value
	fmt.Fprintf(os.Stdout, "  %s  resolution %s\n", ui.Bold(r.Set), r.ID)
	for _, e := range r.Entries {
		fmt.Fprintf(os.Stdout, "    %-28s %s  scan %s  %s\n", fmt.Sprintf("%s@%d", e.Name, e.Version), e.SHA256, e.ScanVerdict, e.ApprovalState)
	}
	if !r.Complete {
		fmt.Fprintln(os.Stdout, "\n  Incomplete. Excluded:")
		for _, e := range r.Excluded {
			fmt.Fprintf(os.Stdout, "    %s@%s: %s\n", e.Name, e.Requested, e.Reason)
		}
	}
	return nil
}

func runSetsCreate(cmd *cobra.Command, args []string) error {
	entries, err := parseSetEntries(args[1:])
	if err != nil {
		return err
	}
	c, err := loadClient()
	if err != nil {
		return err
	}
	res, err := c.CreateSet(args[0], setsCreateDescription, entries)
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	ui.Success("Created set %s with %d %s", res.Value.Name, len(res.Value.Entries), plural(len(res.Value.Entries), "skill", "skills"))
	return nil
}

func runSetsUpdate(cmd *cobra.Command, args []string) error {
	entries, err := parseSetEntries(args[1:])
	if err != nil {
		return err
	}
	descriptionChanged := cmd.Flags().Changed("description")
	if len(entries) == 0 && !descriptionChanged {
		return usageError("usage_error", "nothing to update: give entries or --description", "skael sets update --help")
	}
	c, err := loadClient()
	if err != nil {
		return err
	}
	// The route replaces both fields, so what the caller left out is read first.
	current, err := c.GetSet(args[0])
	if err != nil {
		return apiFailure(err)
	}
	description := current.Value.Description
	if descriptionChanged {
		description = setsUpdateDescription
	}
	if len(entries) == 0 {
		// Kind is derived by the server and read-only, so it is not sent back.
		for _, e := range current.Value.Entries {
			entries = append(entries, client.SetEntry{Name: e.Name, Version: e.Version})
		}
	}
	res, err := c.UpdateSet(args[0], description, entries)
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	ui.Success("Updated set %s", res.Value.Name)
	return nil
}

func runSetsDelete(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	if err := c.DeleteSet(args[0]); err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{"deleted": true, "name": args[0]})
	}
	ui.Success("Deleted set %s", args[0])
	return nil
}
