package contest

import (
	"encoding/json"
	"time"
)

// Provenance says who chose the tasks. It is recorded on every contest and
// shown on every read: a suite authored by someone who owns a candidate, and a
// suite derived from one candidate, are both disclosed rather than refused.
// Refusing either deadlocks the real case, because the people who write suites
// are the people who compete.
type Provenance string

const (
	// ProvenanceAuthored is a reviewed suite. SuiteReviewedBy names who.
	ProvenanceAuthored Provenance = "authored"
	// ProvenanceDerivedAll is generated from every candidate, so no single
	// candidate's claims set the bar.
	ProvenanceDerivedAll Provenance = "derived_all"
	// ProvenanceDerivedOne is generated from one candidate, named in
	// SuiteDerivedFrom. It grades the others against that candidate's claims.
	ProvenanceDerivedOne Provenance = "derived_one"
)

// Status is where a contest is.
type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// DefaultAttempts is the attempts per task per candidate. Three, because one
// attempt makes every task a coin flip and the tie column swallows the result.
const DefaultAttempts = 3

// Candidate is one entry: a published version, and the label the verdict names.
type Candidate struct {
	Position  int             `json:"position"`
	SkillID   string          `json:"-"`
	SkillName string          `json:"skill"`
	Version   int             `json:"version"`
	Label     string          `json:"label"`
	Report    json.RawMessage `json:"report,omitempty"`
}

// TaskLoss is one task a candidate lost, and why. A person reads this to fix
// the skill by hand.
type TaskLoss struct {
	Label    string   `json:"label"`
	TaskID   string   `json:"task_id"`
	Missed   []string `json:"missed"`
	Evidence string   `json:"evidence,omitempty"`
}

// Contest is one comparison, from request to verdict.
type Contest struct {
	ID               string
	SuiteRef         string
	SuiteProvenance  Provenance
	SuiteReviewedBy  string
	SuiteDerivedFrom string
	Tier             string
	Attempts         int
	JobID            string
	Status           Status
	Outcome          Outcome
	Winner           string
	Detail           string
	Pairings         []Pairing
	LastError        string
	RequestedBy      string
	CreatedAt        time.Time
	FinishedAt       *time.Time

	Candidates []Candidate
	Losses     []TaskLoss
}

// IsChainLink reports whether this contest measures one skill against itself.
// The chain is read from these rather than from a stored flag: a flag can
// disagree with the candidates, and the candidates are the fact.
func (c *Contest) IsChainLink() bool {
	if len(c.Candidates) != 2 {
		return false
	}
	return c.Candidates[0].SkillName == c.Candidates[1].SkillName
}

// SuiteNote is the one line every read shows about who chose the tasks.
func (c *Contest) SuiteNote() string {
	switch c.SuiteProvenance {
	case ProvenanceAuthored:
		if c.SuiteReviewedBy != "" {
			return "suite authored, reviewed by " + c.SuiteReviewedBy
		}
		return "suite authored"
	case ProvenanceDerivedOne:
		return "suite derived from " + c.SuiteDerivedFrom + " alone, so it grades the others against that candidate's claims"
	default:
		return "suite derived from every candidate"
	}
}
