package evalqueue

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/sets"
)

// PinnedSet is one stored resolution of a set, as a job installs it. The job
// pins it when it is created, so the result can be reproduced: a re-run with
// the same resolution installs the same versions after a member publishes a
// new one.
type PinnedSet struct {
	SetID        string      `json:"set_id"`
	Name         string      `json:"name"`
	ResolutionID string      `json:"resolution_id"`
	Members      []SetMember `json:"members"`
}

// SetMember is one skill a pinned set installs. SuiteRef names the member's
// authored suite, whose should-trigger queries become probes; it is empty
// when the member has none.
type SetMember struct {
	Name     string `json:"name"`
	Version  int    `json:"version"`
	Digest   string `json:"digest"`
	SuiteRef string `json:"suite_ref,omitempty"`
}

// Errors PinSet returns for a request the caller can fix.
var (
	ErrSetNotFound = errors.New("set not found")
	ErrSetEmpty    = errors.New("the set holds no skill to install")
)

// PinSet resolves a set for a job. With resolutionID it reuses that stored
// resolution; without one it stores the set's current resolution. The skill
// named by without is removed from the members, so the skill under test is
// never installed twice.
func PinSet(ctx context.Context, store *sets.Store, suites *evalsuite.Registry, name, resolutionID, without string) (*PinnedSet, error) {
	set, err := store.Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("evalqueue: pin set %s: %w", name, err)
	}
	if set == nil {
		return nil, fmt.Errorf("%w: %s", ErrSetNotFound, name)
	}
	res, err := store.Pin(ctx, set, resolutionID)
	if err != nil {
		return nil, err
	}
	pin := &PinnedSet{SetID: set.ID, Name: set.Name, ResolutionID: res.ID, Members: []SetMember{}}
	for _, e := range res.Entries {
		if e.Name == without {
			continue
		}
		m := SetMember{Name: e.Name, Version: e.Version, Digest: e.SHA256}
		rec, err := suites.LatestForSkill(ctx, e.Name)
		switch {
		case errors.Is(err, evalsuite.ErrNotFound):
		case err != nil:
			return nil, fmt.Errorf("evalqueue: suite of set member %s: %w", e.Name, err)
		case rec.Origin == evalsuite.OriginAuthored:
			// Only an authored suite's queries are a person's statement of
			// what the member is for.
			m.SuiteRef = rec.Ref
		}
		pin.Members = append(pin.Members, m)
	}
	if len(pin.Members) == 0 {
		return nil, fmt.Errorf("%w: %s resolves to nothing beside %s", ErrSetEmpty, name, without)
	}
	return pin, nil
}

// SetsSince is the first worker release that installs a set. An older worker
// would run the job as a plain eval and report it as one.
var SetsSince = [3]int{0, 24, 0}

// RunsSets reports whether a worker that checked in with version installs a
// job's set.
func RunsSets(version string) bool { return atLeast(version, SetsSince) }

var releaseVersion = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// atLeast reports whether version is since or later. A pre-release or a git
// describe suffix belongs to the release it names; "dev" and an unknown
// worker are older than every release.
func atLeast(version string, since [3]int) bool {
	m := releaseVersion.FindStringSubmatch(version)
	if m == nil {
		return false
	}
	for i := 0; i < 3; i++ {
		n, _ := strconv.Atoi(m[i+1])
		if n != since[i] {
			return n > since[i]
		}
	}
	return true
}
