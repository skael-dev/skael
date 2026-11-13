package hooks

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Event is one activation as POST /api/events receives it.
type Event struct {
	SkillName      string `json:"skill_name"`
	Agent          string `json:"agent"`
	TriggerType    string `json:"trigger_type"`
	ProjectHash    string `json:"project_hash"`
	DeveloperHash  string `json:"developer_hash"`
	EventSource    string `json:"event_source"`
	Model          string `json:"model"`
	HarnessVersion string `json:"harness_version,omitempty"`
	Version        int    `json:"version"`
}

var (
	skillNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$`)
	sessionRe   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	modelRe     = regexp.MustCompile(`^[A-Za-z0-9._:/@-]{1,128}$`)
	transcriptM = regexp.MustCompile(`"model":"([^"]*)"`)
)

const sessionMaxAge = 7 * 24 * time.Hour

// payload reads the agent's hook JSON. Fields that are not strings read as "".
type payload map[string]any

func (p payload) str(keys ...string) string {
	m := map[string]any(p)
	for i, k := range keys {
		v, ok := m[k]
		if !ok {
			return ""
		}
		if i < len(keys)-1 {
			if m, ok = v.(map[string]any); !ok {
				return ""
			}
			continue
		}
		s, _ := v.(string)
		return s
	}
	return ""
}

// first returns the first non-empty field among the dotted paths.
func (p payload) first(paths ...string) string {
	for _, k := range paths {
		if s := p.str(strings.Split(k, ".")...); s != "" {
			return s
		}
	}
	return ""
}

// Report turns one hook payload into the activation to send, or nil when the
// payload is not a skill load. skaelDir holds config.json, state.json and the
// session files.
func Report(agent string, in io.Reader, skaelDir string) *Event {
	raw, err := io.ReadAll(io.LimitReader(in, 4<<20))
	if err != nil {
		return nil
	}
	var p payload
	if json.Unmarshal(raw, &p) != nil || p == nil {
		return nil
	}
	if agent == "cursor" {
		return cursorReport(p, skaelDir)
	}
	return toolReport(agent, p, skaelDir)
}

// toolReport handles agents with a skill tool: Claude Code and Codex.
func toolReport(agent string, p payload, skaelDir string) *Event {
	session := p.str("session_id")
	if !sessionRe.MatchString(session) {
		session = ""
	}
	sessions := filepath.Join(skaelDir, "sessions")
	switch p.str("hook_event_name") {
	case "SessionStart", "PostModelSwitch":
		model := p.first("to_model", "model")
		if session != "" && model != "" {
			if os.MkdirAll(sessions, 0o755) == nil {
				_ = os.WriteFile(filepath.Join(sessions, session), []byte(model), 0o644)
			}
		}
		pruneSessions(sessions)
		return nil
	}

	// Codex has no matcher, so every tool call arrives here. An empty tool
	// name is an unknown payload shape, left to the skill-name checks.
	tool := p.first("tool_name", "tool")
	if tool != "" && tool != "Skill" && tool != "skill" && tool != "skills" && !strings.HasPrefix(tool, "skills_") {
		return nil
	}
	// No generic tool_input.name: it matches other tools' name parameters.
	name := strings.TrimPrefix(p.first("tool_input.skill", "tool_input.skill_name", "tool_input.skillName", "skill_name", "skillName"), "skills_")
	if !validSkill(name) {
		return nil
	}

	model := transcriptModel(p.str("transcript_path"))
	if model == "" && session != "" {
		if b, err := os.ReadFile(filepath.Join(sessions, session)); err == nil {
			model = string(b[:min(len(b), 128)])
		}
	}
	if !modelRe.MatchString(model) {
		model = ""
	}
	wd, _ := os.Getwd()
	return &Event{
		SkillName:     name,
		Agent:         agent,
		TriggerType:   "auto",
		ProjectHash:   shortHash(wd),
		DeveloperHash: developerHash(),
		EventSource:   "tool_invocation",
		Model:         model,
		Version:       installedVersion(skaelDir, name),
	}
}

// cursorSkillRoots are the folders Cursor loads skills from. Cursor has no
// skill tool: one read of <root>/<skill>/SKILL.md is one load.
var cursorSkillRoots = []string{"/.agents/skills", "/.cursor/skills", "/.claude/skills", "/.codex/skills"}

func cursorReport(p payload, skaelDir string) *Event {
	if p.str("tool_name") != "Read" {
		return nil
	}
	file := p.first("tool_input.file_path", "tool_input.path")
	slashed := strings.ReplaceAll(file, `\`, "/")
	if !strings.HasSuffix(slashed, "/SKILL.md") {
		return nil
	}
	skillDir := path.Dir(slashed)
	root := path.Dir(skillDir)
	inRoot := false
	for _, r := range cursorSkillRoots {
		if strings.HasSuffix(root, r) {
			inRoot = true
		}
	}
	if !inRoot {
		return nil
	}

	// A skill skael installed sits in a folder named its canonical name, with
	// ':' as '_' on Windows. Any other skill is named by its frontmatter, then
	// by its folder.
	name := path.Base(skillDir)
	switch canonical := strings.ReplaceAll(name, "_", ":"); {
	case inState(skaelDir, name):
	case inState(skaelDir, canonical):
		name = canonical
	default:
		if fm := frontmatterName(file); fm != "" {
			name = fm
		}
	}
	if !validSkill(name) {
		return nil
	}

	project := ""
	if roots, ok := p["workspace_roots"].([]any); ok && len(roots) > 0 {
		project, _ = roots[0].(string)
	}
	if project == "" {
		project = p.str("cwd")
	}
	if project == "" {
		project, _ = os.Getwd()
	}
	// "default" is Cursor's automatic choice, not a model.
	model := p.first("model_id", "model")
	if model == "default" || !modelRe.MatchString(model) {
		model = ""
	}
	return &Event{
		SkillName:     name,
		Agent:         "cursor",
		TriggerType:   "auto",
		ProjectHash:   shortHash(project),
		DeveloperHash: developerHash(),
		EventSource:   "tool_invocation",
		Model:         model,
		Version:       installedVersion(skaelDir, name),
	}
}

func validSkill(name string) bool {
	return name != "" && name != "null" && len(name) <= 128 && skillNameRe.MatchString(name)
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// developerHash keeps the input the bash hook used, $USER@$HOSTNAME, so a
// developer's hash survives the move to this binary. Windows has USERNAME.
func developerHash() string {
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	if user == "" {
		user = "unknown"
	}
	host := os.Getenv("HOSTNAME")
	if host == "" {
		host, _ = os.Hostname()
	}
	if host == "" {
		host = "unknown"
	}
	return shortHash(user + "@" + host)
}

// transcriptModel is the model on the last assistant line among the last 400
// lines of the transcript.
func transcriptModel(file string) string {
	if file == "" {
		return ""
	}
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	const window = 8 << 20
	if info, err := f.Stat(); err == nil && info.Size() > window {
		_, _ = f.Seek(info.Size()-window, io.SeekStart)
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), window)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > 400 {
			lines = lines[1:]
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if m := transcriptM.FindAllStringSubmatch(lines[i], -1); len(m) > 0 {
			return m[len(m)-1][1]
		}
	}
	return ""
}

type stateFile struct {
	Skills []struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
	} `json:"skills"`
}

func readStateFile(skaelDir string) stateFile {
	var s stateFile
	if b, err := os.ReadFile(filepath.Join(skaelDir, "state.json")); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func inState(skaelDir, name string) bool {
	for _, s := range readStateFile(skaelDir).Skills {
		if s.Name == name {
			return true
		}
	}
	return false
}

// installedVersion is the version the last sync installed. 0 means unknown.
func installedVersion(skaelDir, name string) int {
	for _, s := range readStateFile(skaelDir).Skills {
		if s.Name == name {
			return max(s.Version, 0)
		}
	}
	return 0
}

// frontmatterName is the name field of a SKILL.md's YAML frontmatter.
func frontmatterName(file string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if first {
			if line != "---" {
				return ""
			}
			first = false
			continue
		}
		if line == "---" {
			return ""
		}
		if v, ok := strings.CutPrefix(line, "name:"); ok {
			v = strings.TrimLeft(v, " \t")
			v = strings.TrimPrefix(strings.TrimPrefix(v, `"`), "'")
			v = strings.TrimRight(v, " \t")
			v = strings.TrimSuffix(strings.TrimSuffix(v, `"`), "'")
			return v
		}
	}
	return ""
}

func pruneSessions(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() && time.Since(info.ModTime()) > sessionMaxAge {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
