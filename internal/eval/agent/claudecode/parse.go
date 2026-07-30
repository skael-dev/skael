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

	// rate_limit_event
	RateLimitInfo *rateLimitInfo `json:"rate_limit_info"`
}

// rateLimitInfo is the payload of a rate_limit_event line. A session emits
// one of these routinely as telemetry — "status":"allowed" means the account
// is nowhere near its limit — so only a non-"allowed" status is an actual
// throttle. Treating every rate_limit_event as a hit (as an earlier version
// of this parser did) makes a normal session indistinguishable from one that
// is actually being rate limited, and the runner backs off and eventually
// fails it after exhausting its retries for a limit that was never hit. See
// tests/whetstone/e2e_docker_test.go's stubClaudeBaseTag for the same defect
// caught against a real recorded transcript, and TestParse_RateLimitEventOnlyFlagsAnActualLimit
// in parse_test.go for the regression test.
type rateLimitInfo struct {
	Status string `json:"status"`
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
}

// Parse converts a Claude Code stream-json stream into normalized events.
//
// A malformed line is skipped rather than fatal: a truncated or interleaved
// line must not discard an otherwise complete session, which would turn a
// cosmetic stream defect into a zero score.
//
// The stream is read one line at a time and never buffered in full: tool
// results embed file contents, a session can run to many megabytes, and a
// later phase runs on the order of 60 sessions per evaluation, so holding a
// whole session resident does not scale. The only buffering is a small
// pending queue (below) for the handful of bookkeeping lines that precede the
// first timestamped line.
//
// Timestamp guarantee: no event carries a zero time.Time when the stream
// contains at least one timestamped line — every event before the first
// timestamp is backfilled with it, and every event after carries forward the
// most recently seen one. If the stream contains no timestamp at all (a
// session that failed before its first assistant/user turn — an auth
// failure, an early crash, a stream truncated at line one — is a realistic
// way to produce exactly this), events are returned with the zero time.Time
// rather than a fabricated one. A fabricated timestamp (e.g. time.Now())
// would make Parse non-deterministic across runs on the same recorded
// stream, which this project's reproducibility depends on not happening;
// the zero value is honest and callers can detect it with TS.IsZero() to
// mean "no time information", distinct from any real recorded moment.
func (a *Adapter) Parse(r agent.RawStream) (*agent.Result, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)

	res := &agent.Result{}
	var last time.Time
	var seq int

	// pending holds events built before any timestamp has been seen at all
	// (the bookkeeping lines — hook_started, system/init, thinking_tokens —
	// that precede the first assistant turn and carry no timestamp of their
	// own). They are backfilled with the first real timestamp the moment it
	// arrives and flushed then; from that point on nothing is buffered.
	var pending []trajectory.Event

	// add is the single place an event's timestamp is assigned. While no
	// timestamp has been seen yet (last is zero), the event is queued in
	// pending rather than committed with a zero TS.
	add := func(e trajectory.Event) {
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
			continue // malformed line; keep going
		}

		hadNoTimestampYet := last.IsZero()
		if sl.Timestamp != "" {
			if ts, err := time.Parse(time.RFC3339, sl.Timestamp); err == nil {
				last = ts
			}
		}
		if hadNoTimestampYet && !last.IsZero() {
			// The first real timestamp just arrived: backfill and flush
			// whatever was queued while there was nothing to carry forward.
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
			add(trajectory.Event{Type: trajectory.TypeOpaque, Name: "system/" + sl.Subtype})

		case "rate_limit_event":
			if sl.RateLimitInfo == nil || (sl.RateLimitInfo.Status != "" && sl.RateLimitInfo.Status != "allowed") {
				res.Meta.RateLimited = true
			}
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
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("claudecode.Parse: %w", err)
	}

	// If the stream ended without ever carrying a timestamp, there is nothing
	// truthful to backfill pending with. Flush it as-is — the events keep the
	// zero time.Time (see the timestamp guarantee above) rather than being
	// dropped or stamped with an invented value.
	res.Events = append(res.Events, pending...)

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
//
// Only an explicit exit_code field is trusted. A generic "success" boolean is
// not a shell exit status — it is how a non-shell tool (e.g. a Skill launch
// confirmation, {"success":true,"commandName":"find-skills"}) reports plain
// completion, and inferring ExitCode = 0 from it would hand a later phase a
// shell-style code for a tool call that never had one.
func exitCodeOf(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var obj struct {
		ExitCode *int `json:"exit_code"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return 0, false // string or null form
	}
	if obj.ExitCode == nil {
		return 0, false
	}
	return *obj.ExitCode, true
}
