package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"
)

const maxLineBytes = 8 << 20

type streamLine struct {
	Type       string          `json:"type"`
	Subtype    string          `json:"subtype"`
	Timestamp  string          `json:"timestamp"`
	Message    *apiMessage     `json:"message"`
	ToolResult json.RawMessage `json:"tool_use_result"`

	Version string   `json:"claude_code_version"`
	Model   string   `json:"model"`
	Skills  []string `json:"skills"`

	Usage            *usage   `json:"usage"`
	DurationMS       int64    `json:"duration_ms"`
	NumTurns         int      `json:"num_turns"`
	IsError          bool     `json:"is_error"`
	PermissionDenied []denial `json:"permission_denials"`

	RateLimitInfo *rateLimitInfo `json:"rate_limit_info"`
}

// rateLimitInfo is the payload of a rate_limit_event line. Only a status
// outside the allowed family is an actual throttle — matched by prefix, not
// literal, because "allowed_warning" (emitted past 75% of a window) is not
// a throttle and matching only "allowed" caused a whole tier to retry itself
// to death on sessions that had succeeded.
type rateLimitInfo struct {
	Status        string  `json:"status"`
	Utilization   float64 `json:"utilization"`
	RateLimitType string  `json:"rateLimitType"`
}

const allowedStatusPrefix = "allowed"

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

type toolInput struct {
	FilePath string `json:"file_path"`
	Command  string `json:"command"`
	Skill    string `json:"skill"`
}

// Parse converts a Claude Code stream-json stream into normalized events.
// Malformed lines are skipped, not fatal. Events before the first timestamp
// are backfilled with it; a stream with no timestamp at all keeps the zero
// time.Time rather than a fabricated one.
func (a *ClaudeCode) Parse(r RawStream) (*Result, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)

	res := &Result{}
	var last time.Time
	var seq int
	var pending []Event

	add := func(e Event) {
		seq++
		e.Seq = seq
		if last.IsZero() {
			pending = append(pending, e)
			return
		}
		if e.TS.IsZero() {
			e.TS = last
		}
		res.Events = append(res.Events, e)
	}

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}

		var sl streamLine
		if err := json.Unmarshal(line, &sl); err != nil {
			continue
		}

		hadNoTimestampYet := last.IsZero()
		if sl.Timestamp != "" {
			if ts, err := time.Parse(time.RFC3339, sl.Timestamp); err == nil {
				last = ts
			}
		}
		if hadNoTimestampYet && !last.IsZero() {
			for i := range pending {
				pending[i].TS = last
			}
			res.Events = append(res.Events, pending...)
			pending = nil
		}

		switch sl.Type {
		case "system":
			if sl.Subtype == "init" {
				res.Meta.AgentVersion = sl.Version
				res.Meta.Model = sl.Model
				res.Meta.VisibleSkills = sl.Skills
			}
			add(Event{Type: TypeOpaque, Name: "system/" + sl.Subtype})

		case "rate_limit_event":
			info := sl.RateLimitInfo
			if info == nil || (info.Status != "" && !strings.HasPrefix(info.Status, allowedStatusPrefix)) {
				res.Meta.RateLimited = true
			}
			if info != nil && info.Utilization > res.Meta.RateLimitUtilization {
				res.Meta.RateLimitUtilization = info.Utilization
				res.Meta.RateLimitWindow = info.RateLimitType
			}
			add(Event{Type: TypeOpaque, Name: "rate_limit_event"})

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
			add(Event{Type: TypeOpaque, Name: "result/" + sl.Subtype})

		case "assistant":
			if sl.Message == nil {
				continue
			}
			for _, b := range sl.Message.Content {
				switch b.Type {
				case "text":
					add(Event{Type: TypeMessage})
				case "tool_use":
					add(mapToolUse(b))
				default:
					add(Event{Type: TypeOpaque, Name: "block/" + b.Type})
				}
			}

		case "user":
			if sl.Message == nil {
				continue
			}
			for _, b := range sl.Message.Content {
				if b.Type != "tool_result" {
					add(Event{Type: TypeOpaque, Name: "block/" + b.Type})
					continue
				}
				e := Event{Type: TypeToolResult}
				if code, ok := exitCodeOf(sl.ToolResult); ok {
					e.ExitCode = &code
				}
				add(e)
			}

		default:
			add(Event{Type: TypeOpaque, Name: sl.Type})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("claudecode.Parse: %w", err)
	}

	res.Events = append(res.Events, pending...)
	return res, nil
}

// mapToolUse maps a Claude Code tool call onto the normalized vocabulary.
func mapToolUse(b block) Event {
	var in toolInput
	_ = json.Unmarshal(b.Input, &in)

	e := Event{Name: b.Name}

	switch b.Name {
	case "Read":
		e.Type = TypeFileRead
		e.Paths = pathsOf(in.FilePath)
	case "Write", "Edit", "NotebookEdit":
		e.Type = TypeFileWrite
		e.Paths = pathsOf(in.FilePath)
	case "Bash":
		e.Type = TypeShell
	case "Skill":
		e.Type = TypeToolCall
		e.Paths = []string{path.Join(in.Skill, "SKILL.md")}
	case "AskUserQuestion":
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

// exitCodeOf pulls an exit code from a tool_use_result. Only an explicit
// exit_code field is trusted — a "success" boolean from a non-shell tool
// is not a shell exit status.
func exitCodeOf(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var obj struct {
		ExitCode *int `json:"exit_code"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return 0, false
	}
	if obj.ExitCode == nil {
		return 0, false
	}
	return *obj.ExitCode, true
}
