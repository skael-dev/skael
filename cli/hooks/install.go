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

// autoSyncManagedBy is the marker for auto-sync hooks, separate from activation tracking hooks.
const autoSyncManagedBy = "skael-autosync"

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
		"type":        "command",
		"command":     cmd,
		"_managed_by": managedBy,
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

// InstallClaudeAutoSync adds a UserPromptSubmit hook that runs the debounced auto-sync script.
// This is separate from the activation tracking hook (PreToolUse) — it uses _managed_by: "skael-autosync".
func InstallClaudeAutoSync(configPath, scriptPath string) error {
	settings, err := readJSONFile(configPath)
	if err != nil {
		return err
	}

	cmd := scriptPath

	newHookEntry := map[string]any{
		"type":        "command",
		"command":     cmd,
		"_managed_by": autoSyncManagedBy,
	}

	hooksSection := getOrCreateMap(settings, "hooks")
	settings["hooks"] = hooksSection

	userPromptSubmit := getOrCreateSlice(hooksSection, "UserPromptSubmit")

	// Look for existing skael-autosync entry.
	found := false
	for i, raw := range userPromptSubmit {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if entry["_managed_by"] == autoSyncManagedBy {
			entry["command"] = cmd
			userPromptSubmit[i] = entry
			found = true
			break
		}
	}

	if !found {
		userPromptSubmit = append(userPromptSubmit, newHookEntry)
	}
	hooksSection["UserPromptSubmit"] = userPromptSubmit

	return writeJSONFile(configPath, settings)
}

// UninstallClaudeAutoSync removes the auto-sync hook from configPath.
func UninstallClaudeAutoSync(configPath string) error {
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

	userPromptSubmit, ok := hooksSection["UserPromptSubmit"].([]any)
	if !ok {
		return nil
	}

	var cleaned []any
	for _, raw := range userPromptSubmit {
		entry, ok := raw.(map[string]any)
		if ok && entry["_managed_by"] == autoSyncManagedBy {
			continue
		}
		cleaned = append(cleaned, raw)
	}

	if len(cleaned) == 0 {
		delete(hooksSection, "UserPromptSubmit")
	} else {
		hooksSection["UserPromptSubmit"] = cleaned
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

	codexAutoSyncBlockStart = "# managed_by = skael-autosync"
	codexAutoSyncBlockEnd   = "# end managed_by = skael-autosync"
)

// installCodexHook appends (or replaces) a skael-managed [[hooks.PreToolUse]] TOML block.
//
// The event key is PascalCase (PreToolUse), matching Codex's actual hook event
// names — confirmed against a live third-party hooks.json on this machine and
// against OpenAI's published Codex configuration docs. An older skael wrote the
// snake_case key `pre_tool_use`, under which the hook never fired; that stale
// block is found and replaced (not left alongside the new one) because lookup
// is keyed on the surrounding marker comments, not on the TOML key inside them.
//
// The handler command lives in a nested [[hooks.PreToolUse.hooks]] array-of-
// tables, not as a flat `command` field on [[hooks.PreToolUse]] itself.
// Codex's MatcherGroup type is { matcher: Option<String>, hooks: Vec<...> } —
// a flat `command` field parses as valid TOML but lands nowhere, so Codex
// silently registers zero handlers. Confirmed against openai/codex's own
// config schema (codex-rs/config/src/hook_config.rs) and against the nested
// shape already used by the working third-party hooks.json on this machine.
//
// No `matcher` field is set: Codex supports one, but what tool name a Codex
// skill invocation actually presents is undocumented and unverified on this
// machine, so a guessed pattern is omitted rather than risk silently dropping
// every event.
func installCodexHook(configPath, endpoint, apiKey, scriptPath string) error {
	cmd := fmt.Sprintf("SKAEL_AGENT=codex %s", scriptPath)

	block := fmt.Sprintf("\n%s\n[[hooks.PreToolUse]]\n\n[[hooks.PreToolUse.hooks]]\ntype = \"command\"\ncommand = %q\n%s\n",
		codexBlockStart, cmd, codexBlockEnd)

	existing, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	content := string(existing)

	if hasManagedBlock(content, codexBlockStart) {
		// Replace the existing managed block.
		content = replaceBlock(content, codexBlockStart, codexBlockEnd, block)
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

	content := replaceBlock(string(data), codexBlockStart, codexBlockEnd, "")
	return atomicWriteFile(configPath, []byte(content), 0o644)
}

// hasManagedBlock reports whether content contains a line that, once trimmed,
// is exactly equal to marker. A plain strings.Contains is not safe here:
// codexBlockStart ("# managed_by = skael") is a literal substring of
// codexAutoSyncBlockStart ("# managed_by = skael-autosync"), so a substring
// check against codexBlockStart falsely matches a config that only has an
// autosync block, silently skipping the install of the regular hook. This
// mirrors the exact-line comparison replaceBlock already uses when scanning.
func hasManagedBlock(content, marker string) bool {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == marker {
			return true
		}
	}
	return false
}

// replaceBlock replaces a marker-delimited block (startMarker … endMarker) with replacement.
func replaceBlock(content, startMarker, endMarker, replacement string) string {
	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(content))
	inBlock := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == startMarker {
			inBlock = true
			out.WriteString(replacement)
			continue
		}
		if inBlock {
			if strings.TrimSpace(line) == endMarker {
				inBlock = false
			}
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

// InstallCodexAutoSync appends (or replaces) a skael-autosync managed PreToolUse TOML block.
// See installCodexHook for why the key is PascalCase, why the handler command
// lives in a nested [[hooks.PreToolUse.hooks]] table rather than a flat field,
// and why no matcher is set.
func InstallCodexAutoSync(configPath, scriptPath string) error {
	block := fmt.Sprintf("\n%s\n[[hooks.PreToolUse]]\n\n[[hooks.PreToolUse.hooks]]\ntype = \"command\"\ncommand = %q\n%s\n",
		codexAutoSyncBlockStart, scriptPath, codexAutoSyncBlockEnd)

	existing, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	content := string(existing)
	if hasManagedBlock(content, codexAutoSyncBlockStart) {
		content = replaceBlock(content, codexAutoSyncBlockStart, codexAutoSyncBlockEnd, block)
	} else {
		content += block
	}

	return atomicWriteFile(configPath, []byte(content), 0o644)
}

// UninstallCodexAutoSync removes the skael-autosync TOML block.
func UninstallCodexAutoSync(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	content := replaceBlock(string(data), codexAutoSyncBlockStart, codexAutoSyncBlockEnd, "")
	return atomicWriteFile(configPath, []byte(content), 0o644)
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

	for _, hookName := range []string{"stop"} {
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

// InstallCursorAutoSync adds/updates a sessionStart hook pointing to the auto-sync script.
// This is separate from the activation tracking hook (stop) — it uses _managed_by: "skael-autosync".
func InstallCursorAutoSync(configPath, scriptPath string) error {
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

	// Clean up old-style sync entry (was _managed_by: "skael", now separate).
	if arr, ok := hooksObj["sessionStart"].([]any); ok {
		var cleaned []any
		for _, raw := range arr {
			m, ok := raw.(map[string]any)
			if ok && m["_managed_by"] == managedBy {
				continue // remove old skael entry
			}
			cleaned = append(cleaned, raw)
		}
		hooksObj["sessionStart"] = cleaned
	}

	syncEntry := map[string]any{
		"_managed_by": autoSyncManagedBy,
		"command":     scriptPath,
	}
	upsertCursorAutoSyncEntry(hooksObj, "sessionStart", syncEntry)

	return writeJSONFile(configPath, hooks)
}

// upsertCursorAutoSyncEntry finds the skael-autosync managed entry in the named hook array
// and updates it, or appends a new entry if none exists.
func upsertCursorAutoSyncEntry(hooksObj map[string]any, hookName string, entry map[string]any) {
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
		if m["_managed_by"] == autoSyncManagedBy {
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

// UninstallCursorAutoSync removes the auto-sync sessionStart hook.
func UninstallCursorAutoSync(configPath string) error {
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

	arr, ok := hooksObj["sessionStart"].([]any)
	if !ok {
		return nil
	}

	var filtered []any
	for _, entry := range arr {
		m, ok := entry.(map[string]any)
		if ok && m["_managed_by"] == autoSyncManagedBy {
			continue
		}
		filtered = append(filtered, entry)
	}

	if len(filtered) == 0 {
		delete(hooksObj, "sessionStart")
	} else {
		hooksObj["sessionStart"] = filtered
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

// InstallAutoSyncForAgent calls the appropriate auto-sync installer based on agentName.
func InstallAutoSyncForAgent(agentName, configPath, scriptPath string) error {
	switch agentName {
	case "claude-code":
		return InstallClaudeAutoSync(configPath, scriptPath)
	case "codex":
		return InstallCodexAutoSync(configPath, scriptPath)
	case "cursor":
		return InstallCursorAutoSync(configPath, scriptPath)
	case "opencode":
		// OpenCode auto-sync not yet supported (TypeScript plugin would need rework).
		return nil
	default:
		return fmt.Errorf("unsupported agent: %s", agentName)
	}
}

// UninstallAutoSyncForAgent calls the appropriate auto-sync uninstaller based on agentName.
func UninstallAutoSyncForAgent(agentName, configPath string) error {
	switch agentName {
	case "claude-code":
		return UninstallClaudeAutoSync(configPath)
	case "codex":
		return UninstallCodexAutoSync(configPath)
	case "cursor":
		return UninstallCursorAutoSync(configPath)
	case "opencode":
		return nil
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
