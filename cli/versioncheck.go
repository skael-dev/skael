package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/ui"
)

// versionCheckEvery bounds how often a command asks the server its version.
const versionCheckEvery = 24 * time.Hour

// updateHint names the update command for this OS.
func updateHint() string {
	if runtime.GOOS == "windows" {
		return "Update: scoop update skael, or irm https://skael.dev/install.ps1 | iex"
	}
	return "Update: curl -fsSL https://skael.dev/install | sh, or brew upgrade --cask skael-dev/skael/skael"
}

// versionCheck is the last answer, kept beside the config.
type versionCheck struct {
	CheckedAt     time.Time `json:"checked_at"`
	ServerVersion string    `json:"server_version"`
}

// serverVersion reads the server's version from /api/capabilities, which needs
// no key.
func serverVersion(endpoint string) (string, error) {
	hc := &http.Client{Timeout: 2 * time.Second}
	resp, err := hc.Get(config.NormalizeEndpoint(endpoint) + "/api/capabilities")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("capabilities returned HTTP %d", resp.StatusCode)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Version, nil
}

// parseVersion reads "1.2.3", with or without a leading "v" and with any
// "-suffix" ignored. A dev build has none.
func parseVersion(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	var out [3]int
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// versionNotice compares this CLI with its server. Empty means nothing to say:
// the same version, or a version that cannot be compared.
func versionNotice(cli, server string) string {
	c, ok1 := parseVersion(cli)
	s, ok2 := parseVersion(server)
	if !ok1 || !ok2 {
		return ""
	}
	switch cmp := slices.Compare(c[:], s[:]); {
	case cmp < 0:
		return fmt.Sprintf("skael %s is older than this server (%s). It keeps working, but newer features need an update. %s", cli, server, updateHint())
	case cmp > 0:
		return fmt.Sprintf("This server runs %s, older than this CLI (%s). Features of %s need the server upgraded.", server, cli, cli)
	}
	return ""
}

// maybeNoticeVersion prints a version notice at most once a day. It never
// fails a command: any error is silent. JSON and quiet output stay clean.
func maybeNoticeVersion(args []string) {
	if ui.JSONMode || slices.Contains(args, "--quiet") || slices.Contains(args, "--json") {
		return
	}
	if _, ok := parseVersion(buildVersion); !ok || len(args) == 0 || args[0] == "version" || args[0] == "doctor" || args[0] == "hook" {
		return
	}
	cfg, err := config.LoadConfig()
	if err != nil || cfg.Endpoint == "" {
		return
	}
	path := filepath.Join(config.DefaultDir(), "version-check.json")
	var last versionCheck
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &last)
	}
	if time.Since(last.CheckedAt) < versionCheckEvery {
		return
	}
	server, err := serverVersion(cfg.Endpoint)
	if err != nil {
		return
	}
	if raw, err := json.Marshal(versionCheck{CheckedAt: time.Now().UTC(), ServerVersion: server}); err == nil {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, raw, 0o644)
	}
	if msg := versionNotice(buildVersion, server); msg != "" {
		ui.Warn("%s", msg)
	}
}
