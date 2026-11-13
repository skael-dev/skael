package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var harnessRe = regexp.MustCompile(`[0-9]+\.[0-9]+(\.[0-9]+)?`)

// harnessBins are the CLIs whose version an activation carries.
var harnessBins = map[string]string{"claude-code": "claude", "codex": "codex"}

// Send posts ev to the server in skaelDir's config.json. It runs in the
// detached child, so the agent never waits on the network or on the
// harness's --version.
func Send(ev Event, skaelDir string) error {
	var cfg struct {
		Endpoint string `json:"endpoint"`
		APIKey   string `json:"api_key"`
	}
	raw, err := os.ReadFile(filepath.Join(skaelDir, "config.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.Endpoint == "" || cfg.APIKey == "" {
		return err
	}
	if ev.Agent != "cursor" {
		ev.HarnessVersion = harnessVersion(ev.Agent, skaelDir)
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.Endpoint, "/")+"/api/events", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// harnessVersion is cached for a day: running the agent's CLI costs time.
func harnessVersion(agent, skaelDir string) string {
	bin, ok := harnessBins[agent]
	if !ok {
		return ""
	}
	cache := filepath.Join(skaelDir, "cache", "harness-"+agent)
	if info, err := os.Stat(cache); err == nil && time.Since(info.ModTime()) < 24*time.Hour {
		b, _ := os.ReadFile(cache)
		return validHarness(string(b[:min(len(b), 32)]))
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	hideWindow(cmd)
	out, _ := cmd.Output()
	v := harnessRe.FindString(string(out))
	if os.MkdirAll(filepath.Dir(cache), 0o755) == nil {
		_ = os.WriteFile(cache, []byte(v), 0o644)
	}
	return validHarness(v)
}

func validHarness(v string) string {
	if strings.Trim(v, "0123456789.") != "" {
		return ""
	}
	return v
}

// Detach starts bin with args in a new session, detached from the agent's
// process tree and console, and does not wait for it.
func Detach(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		cmd = exec.Command(bin, args...)
		detachFallback(cmd)
		if err := cmd.Start(); err != nil {
			return err
		}
	}
	return cmd.Process.Release()
}

const autoSyncEvery = 30 * time.Minute

// AutoSyncDue reports whether the last sync recorded in state.json is older
// than 30 minutes, or missing.
func AutoSyncDue(skaelDir string, now time.Time) bool {
	var s struct {
		LastSync string `json:"last_sync"`
	}
	raw, err := os.ReadFile(filepath.Join(skaelDir, "state.json"))
	if err != nil {
		return true
	}
	if json.Unmarshal(raw, &s) != nil || s.LastSync == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339, s.LastSync)
	if err != nil {
		return true
	}
	return now.Sub(last) >= autoSyncEvery
}
