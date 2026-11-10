package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/lint"
)

// OpenCodeVersion is the OpenCode release the sandbox image installs. A run
// records it as its agent version, so a score is never compared across
// releases of the harness. imagespec's Dockerfiles must install this version;
// a test pins the two together.
const OpenCodeVersion = "1.19.5"

// OpenCode adapts the OpenCode CLI, the one harness every panel session runs in.
type OpenCode struct{}

// New returns the OpenCode adapter.
func New() *OpenCode { return &OpenCode{} }

// Name identifies the adapter in reports and panel matrices.
func (a *OpenCode) Name() string { return "opencode" }

// Caps reports OpenCode's capabilities.
func (a *OpenCode) Caps() Caps {
	return Caps{
		EventTier:               "A",
		ModelFlag:               "-m",
		SkillDir:                ".opencode/skills",
		SupportsSkillInvocation: true,
	}
}

// InstallSkill copies a skill bundle into the workspace's project-local
// skill directory.
func (a *OpenCode) InstallSkill(workspace, bundlePath string) error {
	dst := filepath.Join(workspace, a.Caps().SkillDir, filepath.Base(bundlePath))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("opencode.InstallSkill mkdir: %w", err)
	}
	if err := copyTree(bundlePath, dst); err != nil {
		return fmt.Errorf("opencode.InstallSkill copy: %w", err)
	}
	return nil
}

// Argv is the command line one evaluation session runs. Exported so the flags
// are pinned by a test without needing a CLI.
//
// --auto: isolation is the sandbox's job, not the CLI's. Without it OpenCode
// rejects every tool call that would ask for permission, and a headless
// session scores zero with nothing naming the cause.
func Argv(s InvokeSpec) ([]string, error) {
	if strings.TrimSpace(s.Prompt) == "" {
		return nil, errors.New("opencode: invoke has no prompt")
	}
	if strings.TrimSpace(s.Model) == "" {
		return nil, errors.New("opencode: invoke has no model; a run with no model cannot be attributed to a panel member")
	}
	return []string{
		"opencode", "run",
		"--format", "json",
		"--auto",
		New().Caps().ModelFlag, ModelRef(s.Model),
		s.Prompt,
	}, nil
}

// Invoke runs one headless session and returns the native stream verbatim.
func (a *OpenCode) Invoke(ctx context.Context, s InvokeSpec) (RawStream, error) {
	if s.Exec == nil {
		return nil, ErrNoExecutor
	}
	argv, err := Argv(s)
	if err != nil {
		return nil, err
	}
	var out, errBuf bytes.Buffer
	code, err := s.Exec.Exec(ctx, argv, &out, &errBuf)
	if err != nil {
		return nil, fmt.Errorf("opencode: session failed: %w\n%s", err, tail(&errBuf))
	}
	if code != 0 {
		if msg := streamError(out.Bytes()); msg != "" {
			return nil, fmt.Errorf("opencode: session exited %d: %s", code, msg)
		}
		return nil, fmt.Errorf("opencode: session exited %d: %s", code, tail(&out, &errBuf))
	}
	return bytes.NewReader(out.Bytes()), nil
}

func tail(bufs ...*bytes.Buffer) string {
	const max = 512
	var b strings.Builder
	for _, buf := range bufs {
		s := strings.TrimSpace(buf.String())
		if len(s) > max {
			s = "…" + s[len(s)-max:]
		}
		if s != "" {
			b.WriteString(s + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

const maxLineBytes = 8 << 20

// line is one JSON object of `opencode run --format json`.
type line struct {
	Type      string          `json:"type"`
	Timestamp int64           `json:"timestamp"`
	Part      part            `json:"part"`
	Error     json.RawMessage `json:"error"`
}

type part struct {
	Tool   string    `json:"tool"`
	State  toolState `json:"state"`
	Reason string    `json:"reason"`
	Cost   float64   `json:"cost"`
	Tokens *struct {
		Input     int64 `json:"input"`
		Output    int64 `json:"output"`
		Reasoning int64 `json:"reasoning"`
		Cache     struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

type toolState struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Input  struct {
		FilePath string `json:"filePath"`
		Name     string `json:"name"`
	} `json:"input"`
	Metadata struct {
		Exit *int `json:"exit"`
	} `json:"metadata"`
}

// errorMessage reads OpenCode's error object: {"name", "data": {"message"}}.
func errorMessage(raw json.RawMessage) string {
	var e struct {
		Name string `json:"name"`
		Data struct {
			Message    string `json:"message"`
			StatusCode int    `json:"statusCode"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return strings.TrimSpace(string(raw))
	}
	msg := e.Data.Message
	if msg == "" {
		msg = e.Name
	}
	if e.Data.StatusCode != 0 {
		msg = fmt.Sprintf("%s (HTTP %d)", msg, e.Data.StatusCode)
	}
	return msg
}

// streamError returns the first error event's message in a stream, or "".
func streamError(raw []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.Type == "error" {
			return errorMessage(l.Error)
		}
	}
	return ""
}

// Parse converts an OpenCode JSON stream into normalized events. Malformed
// lines are skipped, not fatal.
func (a *OpenCode) Parse(r RawStream) (*Result, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)

	res := &Result{Meta: Meta{AgentVersion: OpenCodeVersion}}
	var first, last time.Time
	seq := 0
	add := func(e Event) {
		seq++
		e.Seq = seq
		res.Events = append(res.Events, e)
	}

	for sc.Scan() {
		var l line
		if len(sc.Bytes()) == 0 || json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		var ts time.Time
		if l.Timestamp > 0 {
			ts = time.UnixMilli(l.Timestamp).UTC()
			if first.IsZero() {
				first = ts
			}
			last = ts
		}
		switch l.Type {
		case "step_start":
			res.Meta.NumTurns++
			add(Event{TS: ts, Type: TypeOpaque, Name: "step_start"})
		case "text":
			add(Event{TS: ts, Type: TypeMessage})
		case "tool_use":
			e := mapTool(l.Part)
			e.TS = ts
			if l.Part.State.Status == "error" && strings.Contains(strings.ToLower(l.Part.State.Error), "permission") {
				res.Meta.PermissionDenials = append(res.Meta.PermissionDenials, l.Part.Tool)
			}
			add(e)
		case "step_finish":
			res.Meta.CostUSD += l.Part.Cost
			if t := l.Part.Tokens; t != nil {
				res.Meta.InputTokens += t.Input + t.Cache.Read + t.Cache.Write
				res.Meta.OutputTokens += t.Output + t.Reasoning
			}
			add(Event{TS: ts, Type: TypeOpaque, Name: "step_finish/" + l.Part.Reason})
		case "error":
			res.Meta.IsError = true
			if res.Meta.Error == "" {
				res.Meta.Error = errorMessage(l.Error)
			}
			add(Event{TS: ts, Type: TypeOpaque, Name: "error"})
		default:
			add(Event{TS: ts, Type: TypeOpaque, Name: l.Type})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("opencode.Parse: %w", err)
	}
	if !first.IsZero() {
		res.Meta.DurationMS = last.Sub(first).Milliseconds()
	}
	return res, nil
}

// mapTool maps an OpenCode tool call onto the normalized vocabulary. The skill
// tool keeps the name "Skill", which is what trigger scoring reads as an
// explicit invocation.
func mapTool(p part) Event {
	in := p.State.Input
	e := Event{Name: p.Tool}
	switch p.Tool {
	case "read":
		e.Type, e.Paths = TypeFileRead, pathsOf(in.FilePath)
	case "write", "edit", "patch", "multiedit":
		e.Type, e.Paths = TypeFileWrite, pathsOf(in.FilePath)
	case "bash":
		e.Type, e.ExitCode = TypeShell, p.State.Metadata.Exit
	case "skill":
		e.Type, e.Name = TypeToolCall, "Skill"
		if in.Name != "" {
			e.Paths = []string{path.Join(in.Name, "SKILL.md")}
		}
	case "question":
		e.Type = TypeAskUser
	default:
		e.Type = TypeToolCall
	}
	return e
}

func pathsOf(p string) []string {
	if p == "" {
		return nil
	}
	return []string{p}
}

// copyTree copies a directory tree, refusing symlinks and filtering through
// lint.Excluded so the eval sidecar (oracle scripts, verifiers) never lands
// in the workspace of the agent being measured.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel != "." && lint.Excluded(filepath.ToSlash(rel)) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case !d.Type().IsRegular():
			return fmt.Errorf("refusing to copy non-regular file %s", rel)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
