package hooks

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// managedBy is the marker value written into hook entries so skael can find them later.
const managedBy = "skael"

// ────────────────────────────────────────────────────────────────────────────
// Claude Code  (JSON settings.json)
// ────────────────────────────────────────────────────────────────────────────

// InstallClaudeHook reads configPath (or starts with an empty object), inserts or
// updates the single skael-managed PreToolUse hook, and writes the file back.
//
// Hook structure:
//
//	{
//	  "hooks": {
//	    "PreToolUse": [
//	      {
//	        "matcher": "Skill",
//	        "hooks": [
//	          {
//	            "type": "command",
//	            "command": "SKAEL_AGENT=claude-code SKAEL_ENDPOINT=... SKAEL_API_KEY=... <scriptPath>",
//	            "_managed_by": "skael"
//	          }
//	        ]
//	      }
//	    ]
//	  }
//	}
func InstallClaudeHook(configPath, endpoint, apiKey, scriptPath string) error {
	settings, err := readJSONFile(configPath)
	if err != nil {
		return err
	}

	cmd := fmt.Sprintf("SKAEL_AGENT=claude-code %s", scriptPath)

	newHookEntry := map[string]any{
		"type":          "command",
		"command":       cmd,
		"_managed_by":   managedBy,
	}

	// Ensure hooks section exists.
	hooksSection := getOrCreateMap(settings, "hooks")
	settings["hooks"] = hooksSection

	// Ensure PreToolUse array exists.
	preToolUse := getOrCreateSlice(hooksSection, "PreToolUse")
	hooksSection["PreToolUse"] = preToolUse

	// Look for an existing skael-managed matcher entry.
	found := false
	for _, raw := range preToolUse {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		innerHooks, ok := entry["hooks"].([]any)
		if !ok {
			continue
		}
		for i, h := range innerHooks {
			hMap, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if hMap["_managed_by"] == managedBy {
				// Update the command in-place.
				hMap["command"] = cmd
				innerHooks[i] = hMap
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		// Append a new matcher entry.
		newEntry := map[string]any{
			"matcher": "Skill",
			"hooks":   []any{newHookEntry},
		}
		hooksSection["PreToolUse"] = append(preToolUse, newEntry)
	}

	return writeJSONFile(configPath, settings)
}

// UninstallClaudeHook removes all hook entries tagged with _managed_by=skael from
// configPath and writes the cleaned file back. Empty arrays/objects are pruned.
func UninstallClaudeHook(configPath string) error {
	settings, err := readJSONFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	hooksSection, ok := settings["hooks"].(map[string]any)
	if !ok {
		return nil
	}

	preToolUse, ok := hooksSection["PreToolUse"].([]any)
	if !ok {
		return nil
	}

	// Filter out any matcher entries that only contain skael-managed hooks,
	// and strip skael-managed inner hooks from entries that mix them.
	var cleaned []any
	for _, raw := range preToolUse {
		entry, ok := raw.(map[string]any)
		if !ok {
			cleaned = append(cleaned, raw)
			continue
		}
		innerHooks, ok := entry["hooks"].([]any)
		if !ok {
			cleaned = append(cleaned, entry)
			continue
		}
		var filteredInner []any
		for _, h := range innerHooks {
			hMap, ok := h.(map[string]any)
			if ok && hMap["_managed_by"] == managedBy {
				continue // remove
			}
			filteredInner = append(filteredInner, h)
		}
		if len(filteredInner) == 0 {
			// Whole entry was skael-managed — drop it.
			continue
		}
		entry["hooks"] = filteredInner
		cleaned = append(cleaned, entry)
	}

	if len(cleaned) == 0 {
		delete(hooksSection, "PreToolUse")
	} else {
		hooksSection["PreToolUse"] = cleaned
	}

	if len(hooksSection) == 0 {
		delete(settings, "hooks")
	}

	return writeJSONFile(configPath, settings)
}

// ────────────────────────────────────────────────────────────────────────────
// Codex CLI  (TOML config.toml)
// ────────────────────────────────────────────────────────────────────────────

const (
	codexBlockStart = "# managed_by = skael"
	codexBlockEnd   = "# end managed_by = skael"
)

// installCodexHook appends (or replaces) a skael-managed [[hooks.pre_tool_use]] TOML block.
func installCodexHook(configPath, endpoint, apiKey, scriptPath string) error {
	cmd := fmt.Sprintf("SKAEL_AGENT=codex %s", scriptPath)

	block := fmt.Sprintf("\n%s\n[[hooks.pre_tool_use]]\ncommand = %q\n%s\n",
		codexBlockStart, cmd, codexBlockEnd)

	existing, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	content := string(existing)

	if strings.Contains(content, codexBlockStart) {
		// Replace the existing managed block.
		content = replaceCodexBlock(content, block)
	} else {
		content += block
	}

	return atomicWriteFile(configPath, []byte(content), 0o644)
}

// uninstallCodexHook removes the skael-managed TOML block from configPath.
func uninstallCodexHook(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	content := replaceCodexBlock(string(data), "")
	return atomicWriteFile(configPath, []byte(content), 0o644)
}

// replaceCodexBlock replaces the skael-managed TOML block with replacement.
func replaceCodexBlock(content, replacement string) string {
	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(content))
	inBlock := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == codexBlockStart {
			inBlock = true
			out.WriteString(replacement)
			continue
		}
		if inBlock {
			if strings.TrimSpace(line) == codexBlockEnd {
				inBlock = false
			}
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

// ────────────────────────────────────────────────────────────────────────────
// OpenCode  (TypeScript plugin file)
// ────────────────────────────────────────────────────────────────────────────

// installOpenCodeHook writes the skael TypeScript plugin to configPath.
// Unlike Claude/Codex, this is a standalone file — not an entry in a shared config.
func installOpenCodeHook(configPath string) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	return atomicWriteFile(configPath, []byte(opencodePlugin), 0o644)
}

// uninstallOpenCodeHook removes the skael TypeScript plugin file.
func uninstallOpenCodeHook(configPath string) error {
	err := os.Remove(configPath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ────────────────────────────────────────────────────────────────────────────
// Cursor  (JSON hooks.json)
// ────────────────────────────────────────────────────────────────────────────

func installCursorHook(configPath, scriptPath string) error {
	hooks, err := readJSONFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read cursor hooks: %w", err)
	}
	if hooks == nil {
		hooks = map[string]any{}
	}

	if _, ok := hooks["version"]; !ok {
		hooks["version"] = float64(1)
	}

	hooksObj, ok := hooks["hooks"].(map[string]any)
	if !ok {
		hooksObj = map[string]any{}
		hooks["hooks"] = hooksObj
	}

	// sessionStart hook: auto-sync skills on project open.
	syncEntry := map[string]any{
		"_managed_by": managedBy,
		"command":     "skael sync --agent cursor --quiet",
	}
	upsertCursorHookEntry(hooksObj, "sessionStart", syncEntry)

	// stop hook: activation tracking via transcript parsing.
	stopCmd := fmt.Sprintf("SKAEL_AGENT=cursor %s", scriptPath)
	stopEntry := map[string]any{
		"_managed_by": managedBy,
		"command":     stopCmd,
	}
	upsertCursorHookEntry(hooksObj, "stop", stopEntry)

	return writeJSONFile(configPath, hooks)
}

// upsertCursorHookEntry finds the skael-managed entry in the named hook array
// and updates it, or appends a new entry if none exists.
func upsertCursorHookEntry(hooksObj map[string]any, hookName string, entry map[string]any) {
	arr, ok := hooksObj[hookName].([]any)
	if !ok {
		arr = []any{}
	}

	found := false
	for i, raw := range arr {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if m["_managed_by"] == managedBy {
			arr[i] = entry
			found = true
			break
		}
	}
	if !found {
		arr = append(arr, entry)
	}

	hooksObj[hookName] = arr
}

func uninstallCursorHook(configPath string) error {
	hooks, err := readJSONFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read cursor hooks: %w", err)
	}

	hooksObj, ok := hooks["hooks"].(map[string]any)
	if !ok {
		return nil
	}

	for _, hookName := range []string{"sessionStart", "stop"} {
		arr, ok := hooksObj[hookName].([]any)
		if !ok {
			continue
		}

		var filtered []any
		for _, entry := range arr {
			m, ok := entry.(map[string]any)
			if ok && m["_managed_by"] == managedBy {
				continue
			}
			filtered = append(filtered, entry)
		}

		if len(filtered) == 0 {
			delete(hooksObj, hookName)
		} else {
			hooksObj[hookName] = filtered
		}
	}

	if len(hooksObj) == 0 {
		delete(hooks, "hooks")
	}

	return writeJSONFile(configPath, hooks)
}

// ────────────────────────────────────────────────────────────────────────────
// Generic dispatch
// ────────────────────────────────────────────────────────────────────────────

// InstallForAgent calls the appropriate installer based on agentName.
func InstallForAgent(agentName, configPath, endpoint, apiKey, scriptPath string) error {
	switch agentName {
	case "claude-code":
		return InstallClaudeHook(configPath, endpoint, apiKey, scriptPath)
	case "codex":
		return installCodexHook(configPath, endpoint, apiKey, scriptPath)
	case "opencode":
		return installOpenCodeHook(configPath)
	case "cursor":
		return installCursorHook(configPath, scriptPath)
	default:
		return fmt.Errorf("unsupported agent: %s", agentName)
	}
}

// UninstallForAgent calls the appropriate uninstaller based on agentName.
func UninstallForAgent(agentName, configPath string) error {
	switch agentName {
	case "claude-code":
		return UninstallClaudeHook(configPath)
	case "codex":
		return uninstallCodexHook(configPath)
	case "opencode":
		return uninstallOpenCodeHook(configPath)
	case "cursor":
		return uninstallCursorHook(configPath)
	default:
		return fmt.Errorf("unsupported agent: %s", agentName)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// JSON helpers
// ────────────────────────────────────────────────────────────────────────────

func readJSONFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return m, nil
}

func writeJSONFile(path string, m map[string]any) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// Preserve existing file permissions; default to 0644 for new files.
	perm := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		perm = info.Mode().Perm()
	}
	return atomicWriteFile(path, data, perm)
}

// atomicWriteFile writes data to path atomically via a .tmp sibling file.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func getOrCreateMap(parent map[string]any, key string) map[string]any {
	if v, ok := parent[key].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

func getOrCreateSlice(parent map[string]any, key string) []any {
	if v, ok := parent[key].([]any); ok {
		return v
	}
	return []any{}
}
