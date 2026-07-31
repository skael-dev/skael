package whetstone

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/cli/client"
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

// maxRequestBodyBytes mirrors internal/server/server.go's
// http.MaxBytesReader(w, r.Body, 10<<20) cap on the whole request body.
const maxRequestBodyBytes = 10 << 20 // 10MB, matches the server's MaxBytesReader limit

// maxArchiveBytes is the largest suite archive that can still fit inside
// maxRequestBodyBytes once base64-encoded into the archive_base64 field:
// base64 inflates size by 4/3, and the rest of the JSON body (skill name,
// checks) is negligible next to it. Checked client-side so an oversized suite
// fails with a plain message instead of an opaque connection/413 error partway
// through the upload.
//
// A var, not a const, so suitepush_internal_test.go can shrink it to exercise
// the guard without generating a multi-megabyte fixture.
var maxArchiveBytes = maxRequestBodyBytes * 3 / 4

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

	checks := make([]client.EvalSuiteCheck, len(rows))
	for i, r := range rows {
		checks[i] = client.EvalSuiteCheck{
			TaskID: r.TaskID,
			OK:     !r.Void,
			Void:   r.Void,
			Reason: r.Reason,
		}
	}

	// LoadSpec's version is what a later score gets compared against. Falling
	// back to some default here would tag the suite against the wrong (or an
	// unknown) spec version and quietly corrupt that comparison, so a failed
	// load fails the push instead.
	//
	// The full spec travels with the suite, not just its version: a
	// published bundle never carries spec.yaml (lint.Excluded strips the
	// whole eval sidecar before packing), so this push is the only chance a
	// worker rebuilding a workspace from a downloaded bundle ever gets to
	// recover the skill's real deps and purpose instead of a placeholder.
	sp, specVersion, err := req.Store.LoadSpec(req.Skill)
	if err != nil {
		return fmt.Errorf("suite push: could not load the spec for %s, so the suite cannot be tagged with a spec version: %w", req.Skill, err)
	}
	specJSON, err := json.Marshal(sp)
	if err != nil {
		return fmt.Errorf("suite push: marshal spec for %s: %w", req.Skill, err)
	}

	archive, err := evalsuite.PackDir(suiteDir)
	if err != nil {
		return fmt.Errorf("suite push: %w", err)
	}
	if len(archive) > maxArchiveBytes {
		return fmt.Errorf("suite push: suite for %s is too large: archive is %d bytes (%d once base64-encoded), exceeding the %d byte limit the server accepts (%d byte request cap); trim tasks or split the suite",
			req.Skill, len(archive), base64EncodedLen(len(archive)), maxArchiveBytes, maxRequestBodyBytes)
	}

	c := client.New(req.Endpoint, req.APIKey)
	resp, err := c.UploadEvalSuite(req.Skill, specVersion, checks, specJSON, archive)
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

// base64EncodedLen returns the length of n bytes once base64-encoded
// (standard encoding, no padding shortcuts assumed).
func base64EncodedLen(n int) int {
	return ((n + 2) / 3) * 4
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
