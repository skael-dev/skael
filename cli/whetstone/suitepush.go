package whetstone

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/ui"
)

var (
	suitePushEndpoint string
	suitePushAPIKey   string
)

var suitePushCmd = &cobra.Command{
	Use:   "push <skill>",
	Short: "Upload a skill's checked evaluation suite to a registry",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = st.Close() }()

		endpoint, apiKey, err := resolveRegistry(suitePushEndpoint, suitePushAPIKey)
		if err != nil {
			return err
		}

		return RunSuitePush(cmd.Context(), SuitePushRequest{
			Store:    st,
			Skill:    args[0],
			Endpoint: endpoint,
			APIKey:   apiKey,
		})
	},
}

// SuitePushRequest holds the inputs to RunSuitePush.
type SuitePushRequest struct {
	Store    *store.Store
	Skill    string
	Endpoint string
	APIKey   string
}

// pushCheck mirrors internal/evalsuite's wire shape for one task's oracle-gate
// result.
type pushCheck struct {
	TaskID string `json:"task_id"`
	OK     bool   `json:"ok"`
	Void   bool   `json:"void,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type pushBody struct {
	Skill         string      `json:"skill"`
	SpecVersion   int         `json:"spec_version"`
	Checks        []pushCheck `json:"checks"`
	ArchiveBase64 string      `json:"archive_base64"`
}

type pushResponse struct {
	Ref       string `json:"ref"`
	TaskCount int    `json:"task_count"`
}

// RunSuitePush uploads a skill's written suite, together with the oracle-gate
// results `whetstone suite check` last recorded for it, to a Skael server. It
// refuses when no check has been recorded for the current suite ref — an
// uploaded suite without check results is not something the server can
// distinguish from a passing one, so this is caught here rather than there.
func RunSuitePush(ctx context.Context, req SuitePushRequest) error {
	suiteDir, err := req.Store.SuiteDir(req.Skill)
	if err != nil {
		return err
	}
	if _, err := os.Stat(suiteDir); err != nil {
		return fmt.Errorf("suite push: no suite found for %s (run `whetstone suite gen %s` first): %w", req.Skill, req.Skill, err)
	}

	suiteRef, err := suite.Ref(suiteDir)
	if err != nil {
		return fmt.Errorf("suite push: %w", err)
	}

	rows, err := req.Store.SuiteChecks(req.Skill, suiteRef)
	if err != nil {
		return fmt.Errorf("suite push: %w", err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("suite push: no suite check recorded for %s; run `whetstone suite check %s` first", req.Skill, req.Skill)
	}

	checks := make([]pushCheck, len(rows))
	for i, r := range rows {
		checks[i] = pushCheck{
			TaskID: r.TaskID,
			OK:     !r.Void,
			Void:   r.Void,
			Reason: r.Reason,
		}
	}

	specVersion := 0
	if _, v, err := req.Store.LoadSpec(req.Skill); err == nil {
		specVersion = v
	}

	archive, err := evalsuite.PackDir(suiteDir)
	if err != nil {
		return fmt.Errorf("suite push: %w", err)
	}

	body := pushBody{
		Skill:         req.Skill,
		SpecVersion:   specVersion,
		Checks:        checks,
		ArchiveBase64: base64.StdEncoding.EncodeToString(archive),
	}

	resp, err := postSuite(ctx, req.Endpoint, req.APIKey, body)
	if err != nil {
		return fmt.Errorf("suite push: %w", err)
	}

	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{
			"skill":      req.Skill,
			"ref":        resp.Ref,
			"task_count": resp.TaskCount,
		})
	}
	ui.Success("pushed %s: %s (%d tasks)", req.Skill, resp.Ref, resp.TaskCount)
	return nil
}

// postSuite POSTs body to {endpoint}/api/eval/suites. cli/client's Client does
// not expose a method for this request shape (its methods are all built
// around form/multipart or fixed JSON shapes for other endpoints), so this
// makes the call directly rather than extend that client for a single new
// body shape.
func postSuite(ctx context.Context, endpoint, apiKey string, body pushBody) (*pushResponse, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/api/eval/suites", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("X-API-Key", apiKey)
	}

	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if httpResp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("server returned %d: %s", httpResp.StatusCode, string(respBody))
	}

	var out pushResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &out, nil
}

// resolveRegistry resolves the endpoint and API key to push to: explicit
// flags first, then SKAEL_ENDPOINT/SKAEL_API_KEY, then the skael CLI's own
// ~/.skael/config.json (read with cli/config's loader rather than parsed
// again here, so the two binaries cannot drift on the file's format).
func resolveRegistry(flagEndpoint, flagAPIKey string) (string, string, error) {
	endpoint := flagEndpoint
	apiKey := flagAPIKey

	if endpoint == "" {
		endpoint = os.Getenv("SKAEL_ENDPOINT")
	}
	if apiKey == "" {
		apiKey = os.Getenv("SKAEL_API_KEY")
	}
	if endpoint != "" && apiKey != "" {
		return endpoint, apiKey, nil
	}

	cfg, err := config.ReadConfig(config.DefaultDir())
	if err != nil {
		return "", "", fmt.Errorf("suite push: no endpoint/key given and could not read %s config: %w", "~/.skael/config.json", err)
	}
	if endpoint == "" {
		endpoint = cfg.Endpoint
	}
	if apiKey == "" {
		apiKey = cfg.APIKey
	}
	if endpoint == "" || apiKey == "" {
		return "", "", fmt.Errorf("suite push: no endpoint/key set; pass --endpoint/--api-key, set SKAEL_ENDPOINT/SKAEL_API_KEY, or run `skael setup`")
	}
	return endpoint, apiKey, nil
}

func init() {
	suitePushCmd.Flags().StringVar(&suitePushEndpoint, "endpoint", "", "Skael server URL (default: $SKAEL_ENDPOINT or ~/.skael/config.json)")
	suitePushCmd.Flags().StringVar(&suitePushAPIKey, "api-key", "", "Skael API key (default: $SKAEL_API_KEY or ~/.skael/config.json)")
	suiteCmd.AddCommand(suitePushCmd)
}
