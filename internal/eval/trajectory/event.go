// Package trajectory defines the normalized event schema every agent adapter
// maps its native stream onto. The drift engine and trigger measurement read
// only this schema, so adding an agent never touches scoring code.
package trajectory

import "time"

// EventType is the normalized event vocabulary.
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
	// recognise. Opaque events are recorded but never scored: they are excluded
	// from contract denominators so that an unmapped event — a new tool, a
	// changed CLI format — cannot be counted as a contract violation.
	TypeOpaque EventType = "opaque"
)

// Event is one normalized step in an agent's trajectory. Arguments and message
// text are digested rather than stored: trajectories are retained for scoring,
// and the raw text can contain workspace contents and credentials.
type Event struct {
	Seq        int       `json:"seq"`
	TS         time.Time `json:"ts"`
	Type       EventType `json:"type"`
	Name       string    `json:"name,omitempty"`
	ArgsDigest string    `json:"args_digest,omitempty"`
	// Paths are the files this event touched. A parser records whatever the
	// agent reported, which is typically an absolute path inside the sandbox
	// container; the runner then calls Relativize so that everything reaching
	// the drift engine is workspace-relative, because contract.MatchPath
	// compares relative patterns and rejects an absolute candidate outright.
	//
	// A path left absolute here is therefore either outside the workspace
	// entirely — which is a real finding and stays visible — or evidence that
	// a code path skipped that relativisation.
	Paths []string `json:"paths,omitempty"`
	// ExitCode is a pointer so that a real exit code of 0 is distinguishable
	// from "no exit code was reported". With a plain int and omitempty, every
	// successful command would serialize as if it had never run.
	ExitCode   *int   `json:"exit_code,omitempty"`
	TextDigest string `json:"text_digest,omitempty"`
}

// Contractable reports whether the event may be evaluated against a contract.
func (e Event) Contractable() bool { return e.Type != TypeOpaque }

// Contractable filters a trajectory down to the events a contract may be
// scored against, preserving order.
func Contractable(events []Event) []Event {
	out := make([]Event, 0, len(events))
	for _, e := range events {
		if e.Contractable() {
			out = append(out, e)
		}
	}
	return out
}
