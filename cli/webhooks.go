package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var webhooksCmd = &cobra.Command{
	Use:   "webhooks",
	Short: "Manage webhook endpoints and their deliveries (owner or admin)",
}

var webhooksListCmd = &cobra.Command{
	Use:   "list",
	Short: "List webhook endpoints",
	Args:  cobra.NoArgs,
	RunE:  runWebhooksList,
}

var webhooksGetCmd = &cobra.Command{
	Use:   "get <endpoint>",
	Short: "Show a webhook endpoint",
	Args:  cobra.ExactArgs(1),
	RunE:  runWebhooksGet,
}

var webhooksCreateCmd = &cobra.Command{
	Use:   "create --name <name> --url <url> --event <type> [--event <type>] [--set <name>]",
	Short: "Register an endpoint and show its secret once",
	Long: `Register an endpoint and show its secret once. The event types are
skill.version.published, skill.version.approved, set.changed and
submission.decided. --set limits set.changed to the sets you name.`,
	Args: cobra.NoArgs,
	RunE: runWebhooksCreate,
}

var webhooksUpdateCmd = &cobra.Command{
	Use:   "update <endpoint> [--name <name>] [--url <url>] [--event <type>] [--set <name>]",
	Short: "Change an endpoint. What you omit stays",
	Long:  "Change an endpoint. Each --event list and --set list replaces the current list. What you omit stays.",
	Args:  cobra.ExactArgs(1),
	RunE:  runWebhooksUpdate,
}

var webhooksDeleteCmd = &cobra.Command{
	Use:   "delete <endpoint>",
	Short: "Delete an endpoint and its delivery log",
	Args:  cobra.ExactArgs(1),
	RunE:  runWebhooksDelete,
}

var webhooksRotateCmd = &cobra.Command{
	Use:   "rotate <endpoint>",
	Short: "Replace an endpoint's secret and show the new one once",
	Long: `Replace an endpoint's secret and show the new one once. Deliveries are signed
with both secrets until the window ends. --window-hours takes 0 to 168, and the
server's default is 24.`,
	Args: cobra.ExactArgs(1),
	RunE: runWebhooksRotate,
}

var webhooksTestCmd = &cobra.Command{
	Use:   "test <endpoint>",
	Short: "Queue a test event to an endpoint",
	Args:  cobra.ExactArgs(1),
	RunE:  runWebhooksTest,
}

var webhooksDeliveriesCmd = &cobra.Command{
	Use:   "deliveries <endpoint>",
	Short: "List an endpoint's newest deliveries and their attempts",
	Args:  cobra.ExactArgs(1),
	RunE:  runWebhooksDeliveries,
}

var webhooksRedeliverCmd = &cobra.Command{
	Use:   "redeliver <delivery-id>",
	Short: "Send a delivery again with the same event id",
	Args:  cobra.ExactArgs(1),
	RunE:  runWebhooksRedeliver,
}

var (
	webhookName        string
	webhookURL         string
	webhookEvents      []string
	webhookSets        []string
	webhookWindowHours int
)

func init() {
	for _, cmd := range []*cobra.Command{webhooksCreateCmd, webhooksUpdateCmd} {
		cmd.Flags().StringVar(&webhookName, "name", "", "A label for the endpoint")
		cmd.Flags().StringVar(&webhookURL, "url", "", "The address that receives deliveries")
		cmd.Flags().StringArrayVar(&webhookEvents, "event", nil, "An event type to subscribe to (repeatable)")
		cmd.Flags().StringArrayVar(&webhookSets, "set", nil, "Limit set.changed to this set (repeatable)")
	}
	for _, flag := range []string{"name", "url", "event"} {
		_ = webhooksCreateCmd.MarkFlagRequired(flag)
	}
	webhooksRotateCmd.Flags().IntVar(&webhookWindowHours, "window-hours", 0, "Hours the old secret stays valid")
	webhooksCmd.AddCommand(webhooksListCmd, webhooksGetCmd, webhooksCreateCmd, webhooksUpdateCmd, webhooksDeleteCmd,
		webhooksRotateCmd, webhooksTestCmd, webhooksDeliveriesCmd, webhooksRedeliverCmd)
	rootCmd.AddCommand(webhooksCmd)
}

// resolveWebhook accepts a name or an id. The API takes only ids, and names are
// not unique, so a shared name asks for the id.
func resolveWebhook(c *client.Client, ref string) (*client.WebhookEndpoint, error) {
	res, err := c.ListWebhookEndpoints()
	if err != nil {
		return nil, apiFailure(err)
	}
	var match *client.WebhookEndpoint
	for i, e := range res.Value.Endpoints {
		if e.ID == ref {
			return &res.Value.Endpoints[i], nil
		}
		if e.Name != ref {
			continue
		}
		if match != nil {
			return nil, usageError("ambiguous_endpoint", fmt.Sprintf("more than one endpoint is named %q", ref), "use the id from skael webhooks list")
		}
		match = &res.Value.Endpoints[i]
	}
	if match == nil {
		return nil, failure("not_found", fmt.Sprintf("webhook endpoint %q not found", ref), "skael webhooks list")
	}
	return match, nil
}

func resolveWebhookSets(c *client.Client, names []string) ([]string, error) {
	ids := make([]string, 0, len(names))
	for _, name := range names {
		set, err := c.GetSet(name)
		if err != nil {
			return nil, apiFailure(err)
		}
		ids = append(ids, set.Value.ID)
	}
	return ids, nil
}

func printWebhookEndpoint(e client.WebhookEndpoint) {
	state := "enabled"
	if !e.Enabled {
		state = "disabled"
	}
	fmt.Fprintf(os.Stdout, "  %s\n", ui.Bold(e.Name))
	fmt.Fprintf(os.Stdout, "    id:     %s\n", e.ID)
	fmt.Fprintf(os.Stdout, "    url:    %s\n", e.URL)
	fmt.Fprintf(os.Stdout, "    state:  %s\n", state)
	fmt.Fprintf(os.Stdout, "    events: %s\n", strings.Join(e.EventTypes, ", "))
	if len(e.SetIDs) > 0 {
		fmt.Fprintf(os.Stdout, "    sets:   %s\n", strings.Join(e.SetIDs, ", "))
	}
}

func runWebhooksList(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	res, err := c.ListWebhookEndpoints()
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	if len(res.Value.Endpoints) == 0 {
		fmt.Fprintln(os.Stdout, "  No webhook endpoints yet.")
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "  Try: skael webhooks create --name <name> --url <url> --event set.changed")
		return nil
	}
	for _, e := range res.Value.Endpoints {
		fmt.Fprintf(os.Stdout, "  %s  %-24s %s\n", e.ID, e.Name, e.URL)
	}
	return nil
}

func runWebhooksGet(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	endpoint, err := resolveWebhook(c, args[0])
	if err != nil {
		return err
	}
	res, err := c.GetWebhookEndpoint(endpoint.ID)
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	printWebhookEndpoint(res.Value)
	return nil
}

func runWebhooksCreate(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	setIDs, err := resolveWebhookSets(c, webhookSets)
	if err != nil {
		return err
	}
	res, err := c.CreateWebhookEndpoint(client.WebhookEndpointInput{Name: webhookName, URL: webhookURL, EventTypes: webhookEvents, SetIDs: setIDs})
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	ui.Success("Created endpoint %s (%s). This is the only time the secret is shown:", res.Value.Name, res.Value.ID)
	fmt.Fprintln(os.Stdout, res.Value.Secret)
	return nil
}

func runWebhooksUpdate(cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()
	if !flags.Changed("name") && !flags.Changed("url") && !flags.Changed("event") && !flags.Changed("set") {
		return usageError("usage_error", "nothing to update: give --name, --url, --event or --set", "skael webhooks update --help")
	}
	c, err := loadClient()
	if err != nil {
		return err
	}
	endpoint, err := resolveWebhook(c, args[0])
	if err != nil {
		return err
	}
	// The route replaces the fields, so what the caller left out is sent back.
	in := client.WebhookEndpointInput{Name: endpoint.Name, URL: endpoint.URL, EventTypes: endpoint.EventTypes, SetIDs: endpoint.SetIDs}
	if flags.Changed("name") {
		in.Name = webhookName
	}
	if flags.Changed("url") {
		in.URL = webhookURL
	}
	if flags.Changed("event") {
		in.EventTypes = webhookEvents
	}
	if flags.Changed("set") {
		if in.SetIDs, err = resolveWebhookSets(c, webhookSets); err != nil {
			return err
		}
	}
	res, err := c.UpdateWebhookEndpoint(endpoint.ID, in)
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	ui.Success("Updated endpoint %s", res.Value.Name)
	return nil
}

func runWebhooksDelete(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	endpoint, err := resolveWebhook(c, args[0])
	if err != nil {
		return err
	}
	if err := c.DeleteWebhookEndpoint(endpoint.ID); err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{"deleted": true, "id": endpoint.ID, "name": endpoint.Name})
	}
	ui.Success("Deleted endpoint %s and its delivery log", endpoint.Name)
	return nil
}

func runWebhooksRotate(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	endpoint, err := resolveWebhook(c, args[0])
	if err != nil {
		return err
	}
	var window *int
	if cmd.Flags().Changed("window-hours") {
		window = &webhookWindowHours
	}
	res, err := c.RotateWebhookSecret(endpoint.ID, window)
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	ui.Success("Rotated the secret of %s. This is the only time it is shown:", endpoint.Name)
	fmt.Fprintln(os.Stdout, res.Value.Secret)
	if res.Value.PreviousExpiresAt != nil {
		ui.Info("The old secret stays valid until %s", res.Value.PreviousExpiresAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	}
	return nil
}

func runWebhooksTest(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	endpoint, err := resolveWebhook(c, args[0])
	if err != nil {
		return err
	}
	if err := c.TestWebhookEndpoint(endpoint.ID); err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{"queued": true, "id": endpoint.ID, "name": endpoint.Name})
	}
	ui.Success("Queued a test event to %s", endpoint.Name)
	ui.Info("Next: skael webhooks deliveries %s", endpoint.ID)
	return nil
}

func runWebhooksDeliveries(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	endpoint, err := resolveWebhook(c, args[0])
	if err != nil {
		return err
	}
	res, err := c.ListWebhookDeliveries(endpoint.ID)
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return printRaw(res.Raw)
	}
	if len(res.Value.Deliveries) == 0 {
		fmt.Fprintf(os.Stdout, "  %s has no deliveries.\n", endpoint.Name)
		return nil
	}
	for _, d := range res.Value.Deliveries {
		fmt.Fprintf(os.Stdout, "  %s  %-24s %-9s event %s\n", d.ID, d.EventType, d.State, d.EventID)
		for _, a := range d.Attempts {
			fmt.Fprintf(os.Stdout, "    %s  %s\n", formatAge(a.CreatedAt), webhookAttemptResult(a))
		}
	}
	return nil
}

func webhookAttemptResult(a client.WebhookAttempt) string {
	switch {
	case a.Succeeded && a.ResponseCode != nil:
		return fmt.Sprintf("delivered, status %d", *a.ResponseCode)
	case a.Error != "":
		return a.Error
	}
	return "failed"
}

func runWebhooksRedeliver(cmd *cobra.Command, args []string) error {
	c, err := loadClient()
	if err != nil {
		return err
	}
	if err := c.RedeliverWebhook(args[0]); err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{"queued": true, "id": args[0]})
	}
	ui.Success("Queued delivery %s again", args[0])
	return nil
}
