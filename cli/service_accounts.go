package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var serviceAccountsCmd = &cobra.Command{
	Use:   "service-accounts",
	Short: "Manage machine identities and their scoped keys (owner or admin)",
}

var serviceAccountsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List service accounts",
	Args:  cobra.NoArgs,
	RunE:  runServiceAccountsList,
}

var serviceAccountsCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a service account",
	Args:  cobra.ExactArgs(1),
	RunE:  runServiceAccountsCreate,
}

var serviceAccountsDeleteCmd = &cobra.Command{
	Use:   "delete <account>",
	Short: "Delete a service account and revoke its keys",
	Args:  cobra.ExactArgs(1),
	RunE:  runServiceAccountsDelete,
}

var serviceKeysCmd = &cobra.Command{
	Use:   "keys",
	Short: "Manage a service account's keys",
}

var serviceKeysListCmd = &cobra.Command{
	Use:   "list <account>",
	Short: "List a service account's keys",
	Args:  cobra.ExactArgs(1),
	RunE:  runServiceKeysList,
}

var serviceKeysCreateCmd = &cobra.Command{
	Use:   "create <account> --scope <scope> [--scope <scope>] [--set <name>]",
	Short: "Create a scoped key and show it once",
	Long: `Create a scoped key and show it once. The scopes are skills:read, sets:read
and eval:work. --set limits the key to one set, and it needs sets:read.`,
	Args: cobra.ExactArgs(1),
	RunE: runServiceKeysCreate,
}

var serviceKeysRevokeCmd = &cobra.Command{
	Use:   "revoke <account> <key-id>",
	Short: "Revoke a key",
	Args:  cobra.ExactArgs(2),
	RunE:  runServiceKeysRevoke,
}

var (
	serviceKeyName   string
	serviceKeyScopes []string
	serviceKeySet    string
)

func init() {
	serviceKeysCreateCmd.Flags().StringVar(&serviceKeyName, "name", "default", "A label for the key")
	serviceKeysCreateCmd.Flags().StringArrayVar(&serviceKeyScopes, "scope", nil, "A scope the key holds (repeatable)")
	serviceKeysCreateCmd.Flags().StringVar(&serviceKeySet, "set", "", "Limit the key to this set")
	_ = serviceKeysCreateCmd.MarkFlagRequired("scope")
	serviceKeysCmd.AddCommand(serviceKeysListCmd, serviceKeysCreateCmd, serviceKeysRevokeCmd)
	serviceAccountsCmd.AddCommand(serviceAccountsListCmd, serviceAccountsCreateCmd, serviceAccountsDeleteCmd, serviceKeysCmd)
	rootCmd.AddCommand(serviceAccountsCmd)
}

// resolveServiceAccount accepts a name or an id. The API takes only ids, and
// people know accounts by name.
func resolveServiceAccount(c *client.Client, ref string) (*client.ServiceAccount, error) {
	res, err := c.ListServiceAccounts()
	if err != nil {
		return nil, apiFailure(err)
	}
	for i, a := range res.Value.ServiceAccounts {
		if a.Name == ref || a.ID == ref {
			return &res.Value.ServiceAccounts[i], nil
		}
	}
	return nil, failure("not_found", fmt.Sprintf("service account %q not found", ref), "skael service-accounts list")
}

func runServiceAccountsList(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	res, err := c.ListServiceAccounts()
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	if len(res.Value.ServiceAccounts) == 0 {
		fmt.Fprintln(os.Stdout, "  No service accounts yet.")
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "  Try: skael service-accounts create <name>")
		return nil
	}
	for _, a := range res.Value.ServiceAccounts {
		lastUsed := "never used"
		if a.LastUsedAt != nil {
			lastUsed = "used " + formatAge(*a.LastUsedAt)
		}
		fmt.Fprintf(os.Stdout, "  %-24s %d %-5s %s\n", a.Name, a.KeyCount, plural(a.KeyCount, "key", "keys"), lastUsed)
	}
	return nil
}

func runServiceAccountsCreate(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	res, err := c.CreateServiceAccount(args[0])
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	ui.Success("Created service account %s", res.Value.Name)
	ui.Info("Next: skael service-accounts keys create %s --scope sets:read", res.Value.Name)
	return nil
}

func runServiceAccountsDelete(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	account, err := resolveServiceAccount(c, args[0])
	if err != nil {
		return err
	}
	if err := c.DeleteServiceAccount(account.ID); err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{"deleted": true, "id": account.ID, "name": account.Name})
	}
	ui.Success("Deleted service account %s and revoked its keys", account.Name)
	return nil
}

func runServiceKeysList(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	account, err := resolveServiceAccount(c, args[0])
	if err != nil {
		return err
	}
	res, err := c.ListServiceKeys(account.ID)
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	if len(res.Value.Keys) == 0 {
		fmt.Fprintf(os.Stdout, "  %s has no keys.\n", account.Name)
		return nil
	}
	for _, k := range res.Value.Keys {
		limit := ""
		if k.SetID != nil {
			limit = "  set " + *k.SetID
		}
		fmt.Fprintf(os.Stdout, "  %s  %-16s %-12s %s%s\n", k.ID, k.Name, k.Prefix, strings.Join(k.Scopes, ","), limit)
	}
	return nil
}

func runServiceKeysCreate(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	account, err := resolveServiceAccount(c, args[0])
	if err != nil {
		return err
	}
	setID := ""
	if serviceKeySet != "" {
		set, err := c.GetSet(serviceKeySet)
		if err != nil {
			return apiFailure(err)
		}
		setID = set.Value.ID
	}
	res, err := c.CreateServiceKey(account.ID, serviceKeyName, serviceKeyScopes, setID)
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	ui.Success("Created key %s for %s. This is the only time it is shown:", res.Value.Name, account.Name)
	fmt.Fprintln(os.Stdout, res.Value.Key)
	return nil
}

func runServiceKeysRevoke(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	account, err := resolveServiceAccount(c, args[0])
	if err != nil {
		return err
	}
	if err := c.RevokeServiceKey(account.ID, args[1]); err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{"revoked": true, "id": args[1]})
	}
	ui.Success("Revoked key %s", args[1])
	return nil
}
