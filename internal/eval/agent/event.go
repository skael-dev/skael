package agent

import "time"

// EventType is the normalized event vocabulary every adapter maps its native
// stream onto, so adding an agent never touches scoring code.
type EventType string

const (
	TypeMessage    EventType = "message"
	TypeToolCall   EventType = "tool_call"
	TypeToolResult EventType = "tool_result"
	TypeShell      EventType = "shell"
	TypeFileRead   EventType = "file_read"
	TypeFileWrite  EventType = "file_write"
	TypeSkillRead  EventType = "skill_read"
	TypeAskUser    EventType = "ask_user"

	// TypeOpaque is what a parser emits for a native event it does not
	// recognise, so a new tool or a changed CLI format is recorded rather than
	// dropped.
	TypeOpaque EventType = "opaque"
)

// Event is one normalized step in an agent's trajectory.
type Event struct {
	Seq  int       `json:"seq"`
	TS   time.Time `json:"ts"`
	Type EventType `json:"type"`
	Name string    `json:"name,omitempty"`
	// Paths are the files this event touched, as the agent reported them.
	// DetectFiring reads the parent directory name, so an absolute container
	// path is as usable here as a workspace-relative one.
	Paths []string `json:"paths,omitempty"`
	// ExitCode is a pointer so that a real exit code of 0 is distinguishable
	// from "no exit code was reported". With a plain int and omitempty, every
	// successful command would serialize as if it had never run.
	ExitCode *int `json:"exit_code,omitempty"`
}
