package claudecode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"time"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

// maxLineBytes bounds one stream line. Tool results embed file contents, so
// lines are large; bufio.Scanner's 64KiB default truncates them silently.
const maxLineBytes = 8 << 20

// streamLine is the union of every top-level event shape. Fields absent from a
// given event type stay zero.
type streamLine struct {
	Type       string          `json:"type"`
	Subtype    string          `json:"subtype"`
	Timestamp  string          `json:"timestamp"`
	Message    *apiMessage     `json:"message"`
	ToolResult json.RawMessage `json:"tool_use_result"`

	// system/init
	Version string   `json:"claude_code_version"`
	Model   string   `json:"model"`
	Skills  []string `json:"skills"`

	// result
	Usage            *usage   `json:"usage"`
	DurationMS       int64    `json:"duration_ms"`
	NumTurns         int      `json:"num_turns"`
	IsError          bool     `json:"is_error"`
	PermissionDenied []denial `json:"permission_denials"`
}

type apiMessage struct {
	Role    string  `json:"role"`
	Content []block `json:"content"`
}

type block struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

type denial struct {
	ToolName string `json:"tool_name"`
}

// toolInput covers the fields the mapper reads across every tool.
type toolInput struct {
	FilePath string `json:"file_path"`
	Command  string `json:"command"`
	Skill    string `json:"skill"`
	Args     string `json:"args"`
}

// Parse converts a Claude Code stream-json stream into normalized events.
//
// A malformed line is skipped rather than fatal: a truncated or interleaved
// line must not discard an otherwise complete session, which would turn a
// cosmetic stream defect into a zero score.
func (a *Adapter) Parse(r agent.RawStream) (*agent.Result, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)

	var lines [][]byte
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		lines = append(lines, append([]byte(nil), line...))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("claudecode.Parse: %w", err)
	}

	res := &agent.Result{}
	var last time.Time
	var seq int

	// Seed `last` with the first timestamp anywhere in the stream. Bookkeeping
	// lines (hook_started, system/init, thinking_tokens) are emitted before the
	// first assistant turn and carry no timestamp of their own; without this
	// seed they would keep the zero time forever, since carry-forward only
	// propagates a timestamp already seen.
	for _, line := range lines {
		var sl streamLine
		if err := json.Unmarshal(line, &sl); err != nil {
			continue
		}
		if sl.Timestamp == "" {
			continue
		}
		if ts, err := time.Parse(time.RFC3339, sl.Timestamp); err == nil {
			last = ts
			break
		}
	}

	// add is the single place an event's timestamp is assigned: every event
	// carries whatever `last` currently is, either its own line's timestamp
	// (just updated below) or the most recently seen one carried forward.
	add := func(e trajectory.Event) {
		seq++
		e.Seq = seq
		if e.TS.IsZero() {
			e.TS = last
		}
		res.Events = append(res.Events, e)
	}

	for _, line := range lines {
		var sl streamLine
		if err := json.Unmarshal(line, &sl); err != nil {
			continue // malformed line; keep going
		}

		if sl.Timestamp != "" {
			if ts, err := time.Parse(time.RFC3339, sl.Timestamp); err == nil {
				last = ts
			}
		}

		switch sl.Type {
		case "system":
			if sl.Subtype == "init" {
				res.Meta.AgentVersion = sl.Version
				res.Meta.Model = sl.Model
				res.Meta.VisibleSkills = sl.Skills
			}
			add(trajectory.Event{Type: trajectory.TypeOpaque, Name: "system/" + sl.Subtype})

		case "rate_limit_event":
			res.Meta.RateLimited = true
			add(trajectory.Event{Type: trajectory.TypeOpaque, Name: "rate_limit_event"})

		case "result":
			res.Meta.DurationMS = sl.DurationMS
			res.Meta.NumTurns = sl.NumTurns
			res.Meta.IsError = sl.IsError
			if sl.Usage != nil {
				res.Meta.InputTokens = sl.Usage.InputTokens
				res.Meta.OutputTokens = sl.Usage.OutputTokens
			}
			for _, d := range sl.PermissionDenied {
				res.Meta.PermissionDenials = append(res.Meta.PermissionDenials, d.ToolName)
			}
			add(trajectory.Event{Type: trajectory.TypeOpaque, Name: "result/" + sl.Subtype})

		case "assistant":
			if sl.Message == nil {
				continue
			}
			for _, b := range sl.Message.Content {
				switch b.Type {
				case "text":
					add(trajectory.Event{Type: trajectory.TypeMessage, TextDigest: trajectory.Digest(b.Text)})
				case "tool_use":
					add(mapToolUse(b))
				default:
					add(trajectory.Event{Type: trajectory.TypeOpaque, Name: "block/" + b.Type})
				}
			}

		case "user":
			if sl.Message == nil {
				continue
			}
			for _, b := range sl.Message.Content {
				if b.Type != "tool_result" {
					add(trajectory.Event{Type: trajectory.TypeOpaque, Name: "block/" + b.Type})
					continue
				}
				e := trajectory.Event{Type: trajectory.TypeToolResult}
				if code, ok := exitCodeOf(sl.ToolResult); ok {
					e.ExitCode = &code
				}
				e.TextDigest = trajectory.Digest(string(sl.ToolResult))
				add(e)
			}

		default:
			add(trajectory.Event{Type: trajectory.TypeOpaque, Name: sl.Type})
		}
	}

	return res, nil
}

// mapToolUse maps a Claude Code tool call onto the normalized vocabulary. Tool
// names are Claude Code's own; anything unrecognised becomes a generic
// tool_call rather than opaque, because it is still real agent behaviour and
// belongs in the focus denominator.
func mapToolUse(b block) trajectory.Event {
	var in toolInput
	_ = json.Unmarshal(b.Input, &in)

	e := trajectory.Event{Name: b.Name, ArgsDigest: trajectory.Digest(string(b.Input))}

	switch b.Name {
	case "Read":
		e.Type = trajectory.TypeFileRead
		e.Paths = pathsOf(in.FilePath)
	case "Write", "Edit", "NotebookEdit":
		e.Type = trajectory.TypeFileWrite
		e.Paths = pathsOf(in.FilePath)
	case "Bash":
		e.Type = trajectory.TypeShell
		e.ArgsDigest = trajectory.Digest(in.Command)
	case "Skill":
		// The native skill-invocation event. Name carries the invoked skill so
		// trigger measurement can match it without path heuristics.
		e.Type = trajectory.TypeSkillRead
		e.Name = in.Skill
	case "AskUserQuestion":
		e.Type = trajectory.TypeAskUser
	default:
		e.Type = trajectory.TypeToolCall
	}
	return e
}

func pathsOf(p string) []string {
	if p == "" {
		return nil
	}
	return []string{p}
}

// exitCodeOf pulls an exit code out of a tool_use_result, which is an object on
// success, a bare JSON string when the tool errored, or null. Only the object
// form can carry a code.
func exitCodeOf(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var obj struct {
		ExitCode *int  `json:"exit_code"`
		Success  *bool `json:"success"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return 0, false // string or null form
	}
	switch {
	case obj.ExitCode != nil:
		return *obj.ExitCode, true
	case obj.Success != nil && *obj.Success:
		return 0, true
	}
	return 0, false
}
