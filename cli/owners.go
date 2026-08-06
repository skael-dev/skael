package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var ownersCmd = &cobra.Command{
	Use:   "owners",
	Short: "Manage skill ownership rules",
}

var ownersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every ownership rule",
	RunE:  runOwnersList,
}

var ownersShowCmd = &cobra.Command{
	Use:   "show <skill-name>",
	Short: "Show the resolved owners for a skill name and which rule matched",
	Args:  cobra.ExactArgs(1),
	RunE:  runOwnersShow,
}

var ownersSetCmd = &cobra.Command{
	Use:   "set <pattern> <email> [email...]",
	Short: "Replace an ownership rule's members wholesale",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runOwnersSet,
}

var ownersAddCmd = &cobra.Command{
	Use:   "add <pattern> <email> [email...]",
	Short: "Add members to an ownership rule, preserving existing ones",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runOwnersAdd,
}

var ownersRmCmd = &cobra.Command{
	Use:   "rm <pattern> <email> [email...]",
	Short: "Remove members from an ownership rule",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runOwnersRm,
}

var ownersDeleteCmd = &cobra.Command{
	Use:   "delete <pattern>",
	Short: "Delete an ownership rule entirely",
	Args:  cobra.ExactArgs(1),
	RunE:  runOwnersDelete,
}

func init() {
	ownersCmd.AddCommand(ownersListCmd, ownersShowCmd, ownersSetCmd, ownersAddCmd, ownersRmCmd, ownersDeleteCmd)
	rootCmd.AddCommand(ownersCmd)
}

// ownersLoadClient loads the CLI config and builds a client, reporting the
// same "not configured" message every other command uses on failure. The
// returned error is non-nil exactly when the caller should return nil
// immediately — the message has already been printed.
func ownersLoadClient() (*client.Client, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError("not configured", "not_configured", "skael setup <url> <api-key>")
			return nil, err
		}
		ui.Error(ui.ErrorDetail{
			Message:    "not configured",
			Suggestion: "skael setup <url> <api-key>",
		})
		return nil, err
	}
	return client.New(cfg.Endpoint, cfg.APIKey), nil
}

// reportOwnersError prints err through the JSON or styled error path,
// matching every other command's convention.
func reportOwnersError(err error, code string) {
	if ui.JSONMode {
		ui.PrintJSONError(err.Error(), code, "")
		return
	}
	ui.Errorf("%s", err)
}

// resolveEmails maps each email to a user id, failing on the first unknown
// one with the near matches the directory returned. A silent skip here would
// write a rule missing an owner, which reads as success and protects nothing.
func resolveEmails(c *client.Client, emails []string) ([]string, error) {
	ids := make([]string, 0, len(emails))
	for _, e := range emails {
		users, err := c.SearchUsers(e)
		if err != nil {
			return nil, err
		}
		var match *client.PublicUser
		for i := range users {
			if strings.EqualFold(users[i].Email, e) {
				match = &users[i]
				break
			}
		}
		if match == nil {
			near := make([]string, 0, len(users))
			for _, u := range users {
				near = append(near, u.Email)
			}
			if len(near) == 0 {
				return nil, fmt.Errorf("no user found matching %q", e)
			}
			return nil, fmt.Errorf("no user with email %q; did you mean: %s", e, strings.Join(near, ", "))
		}
		ids = append(ids, match.ID)
	}
	return ids, nil
}

func runOwnersList(cmd *cobra.Command, args []string) error {
	c, err := ownersLoadClient()
	if err != nil {
		return nil
	}

	rules, err := c.ListOwnershipRules()
	if err != nil {
		reportOwnersError(err, "api_error")
		return nil
	}

	if ui.JSONMode {
		return ui.PrintJSON(map[string]interface{}{"rules": rules})
	}

	if len(rules) == 0 {
		fmt.Fprintln(os.Stdout, "  No ownership rules defined.")
		return nil
	}

	for _, r := range rules {
		fmt.Fprintf(os.Stdout, "  %-24s %s\n", r.Pattern, strings.Join(r.Members, ", "))
	}
	fmt.Fprintf(os.Stdout, "\n  %d %s\n", len(rules), plural(len(rules), "rule", "rules"))
	return nil
}

func runOwnersShow(cmd *cobra.Command, args []string) error {
	name := args[0]

	c, err := ownersLoadClient()
	if err != nil {
		return nil
	}

	res, err := c.SkillOwners(name)
	if err != nil {
		reportOwnersError(err, "api_error")
		return nil
	}

	if ui.JSONMode {
		return ui.PrintJSON(res)
	}

	if res.Unowned {
		fmt.Fprintf(os.Stdout, "  %s is unowned — no rule matches.\n", name)
		return nil
	}

	fmt.Fprintf(os.Stdout, "  %s is owned via rule %s\n", name, res.RulePattern)
	for _, o := range res.Owners {
		fmt.Fprintf(os.Stdout, "    %-28s %s\n", o.Email, o.Name)
	}
	return nil
}

func runOwnersSet(cmd *cobra.Command, args []string) error {
	pattern := args[0]
	emails := args[1:]

	c, err := ownersLoadClient()
	if err != nil {
		return nil
	}

	ids, err := resolveEmails(c, emails)
	if err != nil {
		reportOwnersError(err, "unknown_user")
		return nil
	}

	rule, err := c.UpsertOwnershipRule(pattern, ids)
	if err != nil {
		reportOwnersError(err, "api_error")
		return nil
	}

	if ui.JSONMode {
		return ui.PrintJSON(rule)
	}
	ui.Success("%s now owned by %s", pattern, strings.Join(emails, ", "))
	return nil
}

func runOwnersAdd(cmd *cobra.Command, args []string) error {
	pattern := args[0]
	emails := args[1:]

	c, err := ownersLoadClient()
	if err != nil {
		return nil
	}

	// Resolve every email before touching the current rule set at all: a
	// hard failure here must leave no trace, and never call PUT/POST.
	ids, err := resolveEmails(c, emails)
	if err != nil {
		reportOwnersError(err, "unknown_user")
		return nil
	}

	rules, err := c.ListOwnershipRules()
	if err != nil {
		reportOwnersError(err, "api_error")
		return nil
	}

	members := []string{}
	for _, r := range rules {
		if r.Pattern == pattern {
			members = append(members, r.Members...)
			break
		}
	}
	for _, id := range ids {
		already := false
		for _, m := range members {
			if m == id {
				already = true
				break
			}
		}
		if !already {
			members = append(members, id)
		}
	}

	rule, err := c.UpsertOwnershipRule(pattern, members)
	if err != nil {
		reportOwnersError(err, "api_error")
		return nil
	}

	if ui.JSONMode {
		return ui.PrintJSON(rule)
	}
	ui.Success("added %s to %s", strings.Join(emails, ", "), pattern)
	return nil
}

func runOwnersRm(cmd *cobra.Command, args []string) error {
	pattern := args[0]
	emails := args[1:]

	c, err := ownersLoadClient()
	if err != nil {
		return nil
	}

	ids, err := resolveEmails(c, emails)
	if err != nil {
		reportOwnersError(err, "unknown_user")
		return nil
	}

	rules, err := c.ListOwnershipRules()
	if err != nil {
		reportOwnersError(err, "api_error")
		return nil
	}

	var existing *client.OwnershipRule
	for i := range rules {
		if rules[i].Pattern == pattern {
			existing = &rules[i]
			break
		}
	}
	if existing == nil {
		reportOwnersError(fmt.Errorf("no ownership rule found for pattern %q", pattern), "not_found")
		return nil
	}

	remove := make(map[string]bool, len(ids))
	for _, id := range ids {
		remove[id] = true
	}
	members := make([]string, 0, len(existing.Members))
	for _, m := range existing.Members {
		if !remove[m] {
			members = append(members, m)
		}
	}

	rule, err := c.UpsertOwnershipRule(pattern, members)
	if err != nil {
		reportOwnersError(err, "api_error")
		return nil
	}

	if ui.JSONMode {
		return ui.PrintJSON(rule)
	}
	ui.Success("removed %s from %s", strings.Join(emails, ", "), pattern)
	return nil
}

func runOwnersDelete(cmd *cobra.Command, args []string) error {
	pattern := args[0]

	c, err := ownersLoadClient()
	if err != nil {
		return nil
	}

	rules, err := c.ListOwnershipRules()
	if err != nil {
		reportOwnersError(err, "api_error")
		return nil
	}

	var id string
	for _, r := range rules {
		if r.Pattern == pattern {
			id = r.ID
			break
		}
	}
	if id == "" {
		reportOwnersError(fmt.Errorf("no ownership rule found for pattern %q", pattern), "not_found")
		return nil
	}

	if err := c.DeleteOwnershipRule(id); err != nil {
		reportOwnersError(err, "api_error")
		return nil
	}

	if ui.JSONMode {
		return ui.PrintJSON(map[string]interface{}{"deleted": true, "pattern": pattern})
	}
	ui.Success("deleted ownership rule for %s", pattern)
	return nil
}
