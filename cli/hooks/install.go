package hooks

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// managedBy marks the activation hook entries skael writes.
const managedBy = "skael"

// autoSyncManagedBy marks the auto-sync entries, so each kind installs and
// uninstalls on its own.
const autoSyncManagedBy = "skael-autosync"

// edit turns an agent config file's bytes into the bytes skael wants there.
// A missing file is nil.
type edit func([]byte) ([]byte, error)

// applyEdit writes path only when the edit changes it, and deletes it when
// the edit returns nil. With create false, a missing file stays missing.
func applyEdit(path string, e edit, create bool) error {
	data, err := os.ReadFile(path)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return err
	}
	if missing && !create {
		return nil
	}
	out, err := e(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if out == nil {
		if missing {
			return nil
		}
		return os.Remove(path)
	}
	if !missing && bytes.Equal(out, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		perm = info.Mode().Perm()
	}
	return atomicWriteFile(path, out, perm)
}

// ────────────────────────────────────────────────────────────────────────────
// Claude Code  (settings.json)
// ────────────────────────────────────────────────────────────────────────────

// claudeEvents are the events `hook report` handles: the Skill tool call, and
// the session start and model switch that name a session's model.
var claudeEvents = []struct{ name, matcher string }{
	{"PreToolUse", "Skill"},
	{"SessionStart", ""},
	{"PostModelSwitch", ""},
}

// claudeHandler is exec form: Claude Code spawns bin with args and no shell,
// so a path with spaces needs no quoting and Git Bash is not required.
func claudeHandler(bin string, args []string, marker string) map[string]any {
	list := make([]any, len(args))
	for i, a := range args {
		list[i] = a
	}
	return map[string]any{"type": "command", "command": bin, "args": list, "_managed_by": marker}
}

func claudeActivation(bin string) edit {
	return jsonEdit(func(settings map[string]any) {
		hooks := getOrCreateMap(settings, "hooks")
		settings["hooks"] = hooks
		for _, ev := range claudeEvents {
			upsertClaudeHook(hooks, ev.name, ev.matcher, claudeHandler(bin, reportArgs("claude-code"), managedBy))
		}
	})
}

func claudeAutoSync(bin string) edit {
	return jsonEdit(func(settings map[string]any) {
		hooks := getOrCreateMap(settings, "hooks")
		settings["hooks"] = hooks
		dropFlatAutoSync(hooks)
		upsertClaudeHook(hooks, "UserPromptSubmit", "", claudeHandler(bin, autoSyncArgs, autoSyncManagedBy))
	})
}

func claudeRemove(marker string, events ...string) edit {
	return jsonEdit(func(settings map[string]any) {
		hooks, ok := settings["hooks"].(map[string]any)
		if !ok {
			return
		}
		if marker == autoSyncManagedBy {
			dropFlatAutoSync(hooks)
		}
		for _, ev := range events {
			removeClaudeHooks(hooks, ev, marker)
		}
		if len(hooks) == 0 {
			delete(settings, "hooks")
		}
	})
}

func claudeEventNames() []string {
	names := make([]string, len(claudeEvents))
	for i, ev := range claudeEvents {
		names[i] = ev.name
	}
	return names
}

// upsertClaudeHook replaces the handler carrying the handler's marker under
// event, or appends a new matcher group for it.
func upsertClaudeHook(hooks map[string]any, event, matcher string, handler map[string]any) {
	list := getOrCreateSlice(hooks, event)
	for _, raw := range list {
		group, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		inner, ok := group["hooks"].([]any)
		if !ok {
			continue
		}
		for i, h := range inner {
			if m, ok := h.(map[string]any); ok && m["_managed_by"] == handler["_managed_by"] {
				inner[i] = handler
				hooks[event] = list
				return
			}
		}
	}
	group := map[string]any{"hooks": []any{handler}}
	if matcher != "" {
		group["matcher"] = matcher
	}
	hooks[event] = append(list, group)
}

// removeClaudeHooks strips the handlers carrying marker under event, and the
// groups and the event left empty.
func removeClaudeHooks(hooks map[string]any, event, marker string) {
	list, ok := hooks[event].([]any)
	if !ok {
		return
	}
	var cleaned []any
	for _, raw := range list {
		group, ok := raw.(map[string]any)
		if !ok {
			cleaned = append(cleaned, raw)
			continue
		}
		inner, ok := group["hooks"].([]any)
		if !ok {
			cleaned = append(cleaned, group)
			continue
		}
		var kept []any
		for _, h := range inner {
			if m, ok := h.(map[string]any); ok && m["_managed_by"] == marker {
				continue
			}
			kept = append(kept, h)
		}
		if len(kept) == 0 {
			continue
		}
		group["hooks"] = kept
		cleaned = append(cleaned, group)
	}
	if len(cleaned) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = cleaned
	}
}

// dropFlatAutoSync removes the auto-sync handler older releases wrote straight
// into UserPromptSubmit, outside a matcher group, where Claude Code never ran it.
func dropFlatAutoSync(hooks map[string]any) {
	list, ok := hooks["UserPromptSubmit"].([]any)
	if !ok {
		return
	}
	var kept []any
	for _, raw := range list {
		if m, ok := raw.(map[string]any); ok && m["_managed_by"] == autoSyncManagedBy {
			continue
		}
		kept = append(kept, raw)
	}
	if len(kept) == 0 {
		delete(hooks, "UserPromptSubmit")
	} else {
		hooks["UserPromptSubmit"] = kept
	}
}

func claudeHas(data []byte, marker string) bool {
	settings, err := parseJSON(data)
	if err != nil {
		return false
	}
	hooks, _ := settings["hooks"].(map[string]any)
	for _, rawList := range hooks {
		list, _ := rawList.([]any)
		for _, raw := range list {
			group, _ := raw.(map[string]any)
			if group["_managed_by"] == marker {
				return true
			}
			inner, _ := group["hooks"].([]any)
			for _, h := range inner {
				if m, ok := h.(map[string]any); ok && m["_managed_by"] == marker {
					return true
				}
			}
		}
	}
	return false
}

// ────────────────────────────────────────────────────────────────────────────
// Codex CLI  (config.toml)
// ────────────────────────────────────────────────────────────────────────────

const (
	codexBlockStart = "# managed_by = skael"
	codexBlockEnd   = "# end managed_by = skael"

	codexAutoSyncBlockStart = "# managed_by = skael-autosync"
	codexAutoSyncBlockEnd   = "# end managed_by = skael-autosync"
)

// codexBlock is one PreToolUse matcher group. The event key is PascalCase and
// the handler sits in a nested hooks table, which is the shape Codex parses; a
// flat command field parses as TOML and registers nothing. No matcher is set,
// because the tool name a Codex skill call presents is undocumented, so
// `hook report` filters instead. Codex runs `command` in the session shell and
// `command_windows` on Windows, where that shell is PowerShell.
func codexBlock(start, end, bin string, args []string) string {
	return fmt.Sprintf("%s\n[[hooks.PreToolUse]]\n\n[[hooks.PreToolUse.hooks]]\ntype = \"command\"\ncommand = %q\ncommand_windows = %q\n%s\n",
		start, posixCommand(bin, args), windowsCommand(bin, args), end)
}

func codexUpsert(start, end, block string) edit {
	return func(data []byte) ([]byte, error) {
		content := string(data)
		if hasManagedBlock(content, start) {
			return []byte(replaceBlock(content, start, end, block)), nil
		}
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		if content != "" {
			content += "\n"
		}
		return []byte(content + block), nil
	}
}

func codexRemove(start, end string) edit {
	return func(data []byte) ([]byte, error) {
		return []byte(replaceBlock(string(data), start, end, "")), nil
	}
}

// hasManagedBlock compares whole trimmed lines: codexBlockStart is a
// substring of codexAutoSyncBlockStart.
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

// ────────────────────────────────────────────────────────────────────────────
// Cursor  (hooks.json)
// ────────────────────────────────────────────────────────────────────────────

// Cursor runs a hook command through a shell: PowerShell on Windows.
func cursorActivation(bin string) edit {
	return cursorEdit(func(hooks map[string]any) {
		removeCursorEntries(hooks, managedBy, "stop")
		upsertCursorEntry(hooks, "postToolUse", map[string]any{
			"_managed_by": managedBy,
			"command":     shellCommand(bin, reportArgs("cursor")),
			"matcher":     "Read",
		})
	})
}

func cursorAutoSync(bin string) edit {
	return cursorEdit(func(hooks map[string]any) {
		removeCursorEntries(hooks, managedBy, "sessionStart")
		upsertCursorEntry(hooks, "sessionStart", map[string]any{
			"_managed_by": autoSyncManagedBy,
			"command":     shellCommand(bin, autoSyncArgs),
		})
	})
}

func cursorRemove(marker string, events ...string) edit {
	return jsonEdit(func(file map[string]any) {
		hooks, ok := file["hooks"].(map[string]any)
		if !ok {
			return
		}
		removeCursorEntries(hooks, marker, events...)
		if len(hooks) == 0 {
			delete(file, "hooks")
		}
	})
}

func cursorEdit(f func(hooks map[string]any)) edit {
	return jsonEdit(func(file map[string]any) {
		if _, ok := file["version"]; !ok {
			file["version"] = float64(1)
		}
		hooks := getOrCreateMap(file, "hooks")
		file["hooks"] = hooks
		f(hooks)
	})
}

func upsertCursorEntry(hooks map[string]any, event string, entry map[string]any) {
	list := getOrCreateSlice(hooks, event)
	for i, raw := range list {
		if m, ok := raw.(map[string]any); ok && m["_managed_by"] == entry["_managed_by"] {
			list[i] = entry
			hooks[event] = list
			return
		}
	}
	hooks[event] = append(list, entry)
}

func removeCursorEntries(hooks map[string]any, marker string, events ...string) {
	for _, event := range events {
		list, ok := hooks[event].([]any)
		if !ok {
			continue
		}
		var kept []any
		for _, raw := range list {
			if m, ok := raw.(map[string]any); ok && m["_managed_by"] == marker {
				continue
			}
			kept = append(kept, raw)
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
}

func cursorHas(data []byte, marker string) bool {
	file, err := parseJSON(data)
	if err != nil {
		return false
	}
	hooks, _ := file["hooks"].(map[string]any)
	for _, rawList := range hooks {
		list, _ := rawList.([]any)
		for _, raw := range list {
			if m, ok := raw.(map[string]any); ok && m["_managed_by"] == marker {
				return true
			}
		}
	}
	return false
}

// ────────────────────────────────────────────────────────────────────────────
// Dispatch
// ────────────────────────────────────────────────────────────────────────────

// target is one file skael manages for an agent, and the edits for it.
type target struct {
	path    string
	install edit
	remove  edit
	has     func([]byte) bool
	// normalize is what the file reads as once skael rewrites it unchanged,
	// so a hand-formatted JSON file still counts as current.
	normalize edit
}

func activationTarget(agentName, configPath, bin string) (target, error) {
	switch agentName {
	case "claude-code":
		return target{configPath, claudeActivation(bin), claudeRemove(managedBy, claudeEventNames()...),
			func(b []byte) bool { return claudeHas(b, managedBy) }, jsonEdit(func(map[string]any) {})}, nil
	case "codex":
		return target{configPath, codexUpsert(codexBlockStart, codexBlockEnd, codexBlock(codexBlockStart, codexBlockEnd, bin, reportArgs("codex"))),
			codexRemove(codexBlockStart, codexBlockEnd),
			func(b []byte) bool { return hasManagedBlock(string(b), codexBlockStart) }, identity}, nil
	case "opencode":
		return pluginTarget(configPath, opencodePlugin), nil
	case "pi":
		return pluginTarget(configPath, piExtension), nil
	case "cursor":
		return target{configPath, cursorActivation(bin), cursorRemove(managedBy, "stop", "postToolUse"),
			func(b []byte) bool { return cursorHas(b, managedBy) }, jsonEdit(func(map[string]any) {})}, nil
	}
	return target{}, fmt.Errorf("unsupported agent: %s", agentName)
}

func autoSyncTarget(agentName, configPath, bin string) (target, error) {
	switch agentName {
	case "claude-code":
		return target{configPath, claudeAutoSync(bin), claudeRemove(autoSyncManagedBy, "UserPromptSubmit"),
			func(b []byte) bool { return claudeHas(b, autoSyncManagedBy) }, jsonEdit(func(map[string]any) {})}, nil
	case "codex":
		return target{configPath, codexUpsert(codexAutoSyncBlockStart, codexAutoSyncBlockEnd, codexBlock(codexAutoSyncBlockStart, codexAutoSyncBlockEnd, bin, autoSyncArgs)),
			codexRemove(codexAutoSyncBlockStart, codexAutoSyncBlockEnd),
			func(b []byte) bool { return hasManagedBlock(string(b), codexAutoSyncBlockStart) }, identity}, nil
	case "opencode", "pi":
		return pluginTarget(autoSyncPluginPath(configPath), autoSyncTS(agentName, bin)), nil
	case "cursor":
		return target{configPath, cursorAutoSync(bin), cursorRemove(autoSyncManagedBy, "sessionStart"),
			func(b []byte) bool { return cursorHas(b, autoSyncManagedBy) }, jsonEdit(func(map[string]any) {})}, nil
	}
	return target{}, fmt.Errorf("unsupported agent: %s", agentName)
}

func identity(b []byte) ([]byte, error) { return b, nil }

// InstallForAgent registers `skael hook report` with one agent.
func InstallForAgent(agentName, configPath, bin string) error {
	t, err := activationTarget(agentName, configPath, bin)
	if err != nil {
		return err
	}
	return applyEdit(t.path, t.install, true)
}

// UninstallForAgent removes the activation hook from one agent.
func UninstallForAgent(agentName, configPath string) error {
	t, err := activationTarget(agentName, configPath, "")
	if err != nil {
		return err
	}
	return applyEdit(t.path, t.remove, false)
}

// InstallAutoSyncForAgent registers `skael hook autosync` with one agent.
func InstallAutoSyncForAgent(agentName, configPath, bin string) error {
	t, err := autoSyncTarget(agentName, configPath, bin)
	if err != nil {
		return err
	}
	return applyEdit(t.path, t.install, true)
}

// UninstallAutoSyncForAgent removes the auto-sync hook from one agent.
func UninstallAutoSyncForAgent(agentName, configPath string) error {
	t, err := autoSyncTarget(agentName, configPath, "")
	if err != nil {
		return err
	}
	return applyEdit(t.path, t.remove, false)
}

// HookState says which skael hooks an agent's config holds, and whether each
// names bin with the current subcommand.
type HookState struct {
	Activation, ActivationCurrent bool
	AutoSync, AutoSyncCurrent     bool
}

// State reads an agent's config and reports its skael hooks.
func State(agentName, configPath, bin string) HookState {
	var s HookState
	if t, err := activationTarget(agentName, configPath, bin); err == nil {
		s.Activation, s.ActivationCurrent = t.state()
	}
	if t, err := autoSyncTarget(agentName, configPath, bin); err == nil {
		s.AutoSync, s.AutoSyncCurrent = t.state()
	}
	return s
}

func (t target) state() (present, current bool) {
	data, err := os.ReadFile(t.path)
	if err != nil || !t.has(data) {
		return false, false
	}
	want, err := t.install(data)
	if err != nil {
		return true, false
	}
	have, err := t.normalize(data)
	return true, err == nil && bytes.Equal(want, have)
}

// Repair rewrites the skael hooks an agent already has that name another
// binary or an old script. It adds no hook the user did not install.
func Repair(agentName, configPath, bin string) error {
	s := State(agentName, configPath, bin)
	var errs []error
	if s.Activation && !s.ActivationCurrent {
		errs = append(errs, InstallForAgent(agentName, configPath, bin))
	}
	if s.AutoSync && !s.AutoSyncCurrent {
		errs = append(errs, InstallAutoSyncForAgent(agentName, configPath, bin))
	}
	return errors.Join(errs...)
}

// RemoveLegacyScripts deletes the bash hook scripts older releases wrote.
func RemoveLegacyScripts(skaelDir string) {
	for _, name := range []string{"skael-hook.sh", "skael-autosync.sh", "skael-cursor-read.sh", "skael-cursor-stop.sh"} {
		_ = os.Remove(filepath.Join(skaelDir, "hooks", name))
	}
	_ = os.Remove(filepath.Join(skaelDir, "hooks"))
}

// ────────────────────────────────────────────────────────────────────────────
// JSON helpers
// ────────────────────────────────────────────────────────────────────────────

func jsonEdit(f func(map[string]any)) edit {
	return func(data []byte) ([]byte, error) {
		m, err := parseJSON(data)
		if err != nil {
			return nil, err
		}
		f(m)
		return json.MarshalIndent(m, "", "  ")
	}
}

func parseJSON(data []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
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
