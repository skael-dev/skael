package agent

import "time"

// EventType is the normalized event vocabulary every adapter maps onto.
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
	TypeOpaque     EventType = "opaque"
)

// Event is one normalized step in an agent's trajectory.
type Event struct {
	Seq   int       `json:"seq"`
	TS    time.Time `json:"ts"`
	Type  EventType `json:"type"`
	Name  string    `json:"name,omitempty"`
	Paths []string  `json:"paths,omitempty"`
	// ExitCode is a pointer so 0 is distinguishable from "not reported".
	ExitCode *int `json:"exit_code,omitempty"`
}
