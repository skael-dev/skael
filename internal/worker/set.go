package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/skill"
)

// SetInput is a pinned set, unpacked for one run.
type SetInput struct {
	Name         string
	ResolutionID string
	// Members are installed beside the skill.
	Members []SetMemberInput
	// Pinned is every skill of the resolution the job pinned, for the
	// report. For a set candidate it includes the skill the run is filed
	// under, which Members leaves out.
	Pinned []evalqueue.SetMember
	// Candidate is true when the skill and the members together are one
	// contest candidate.
	Candidate bool
}

// SetMemberInput is one member of a set, unpacked at Dir. The base of Dir is
// the folder the member is installed under.
type SetMemberInput struct {
	Name    string
	Version int
	Digest  string
	Dir     string
	// Queries are the should-trigger queries of the member's authored suite.
	Queries []string
	// Deps are the packages the member's suite spec declares.
	Deps spec.DepsDecl
}

// fetchMembers downloads every member's archive and checks it against the
// digest the resolution pinned. A mismatch is refused: the run would install
// something the resolution never named.
func (w *Worker) fetchMembers(ctx context.Context, pin *evalqueue.PinnedSet) ([][]byte, error) {
	out := make([][]byte, 0, len(pin.Members))
	for _, m := range pin.Members {
		b, err := w.api.FetchBundle(ctx, m.Name, m.Version)
		if err != nil {
			return nil, fmt.Errorf("worker: fetch set member %s@%d: %w", m.Name, m.Version, err)
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != m.Digest {
			return nil, fmt.Errorf("worker: set member %s@%d has digest %s, the resolution pinned %s", m.Name, m.Version, got, m.Digest)
		}
		out = append(out, b)
	}
	return out, nil
}

// unpackMembers unpacks members[i] from bundles[i] under root, each in its own
// folder, and reads each member's authored suite for its queries and deps.
// taken holds the folders already used, such as the skill under test's.
func (w *Worker) unpackMembers(ctx context.Context, root string, members []evalqueue.SetMember, bundles [][]byte, taken map[string]bool) ([]SetMemberInput, error) {
	out := make([]SetMemberInput, 0, len(members))
	for i, m := range members {
		dir := filepath.Join(root, installFolder(m.Name, taken))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("worker: set member dir: %w", err)
		}
		if err := skill.Unpack(bytes.NewReader(bundles[i]), dir); err != nil {
			return nil, fmt.Errorf("worker: unpack set member %s: %w", m.Name, err)
		}
		in := SetMemberInput{Name: m.Name, Version: m.Version, Digest: m.Digest, Dir: dir}
		if m.SuiteRef != "" {
			queries, deps, err := w.memberSuite(ctx, root, m.SuiteRef)
			if err != nil {
				return nil, fmt.Errorf("worker: suite of set member %s: %w", m.Name, err)
			}
			in.Queries, in.Deps = queries, deps
		}
		out = append(out, in)
	}
	return out, nil
}

// memberSuite returns a member suite's should-trigger queries and the deps
// its spec declares. A suite from a skill folder has no spec and no deps.
func (w *Worker) memberSuite(ctx context.Context, root, ref string) ([]string, spec.DepsDecl, error) {
	archive, err := w.api.FetchSuite(ctx, ref)
	if err != nil {
		return nil, spec.DepsDecl{}, err
	}
	meta, err := w.api.SuiteMeta(ctx, ref)
	if err != nil {
		return nil, spec.DepsDecl{}, err
	}
	dir, err := os.MkdirTemp(root, "suite-*")
	if err != nil {
		return nil, spec.DepsDecl{}, err
	}
	defer os.RemoveAll(dir)
	if err := evalsuite.Unpack(archive, dir); err != nil {
		return nil, spec.DepsDecl{}, err
	}
	triggers, err := suite.LoadTriggerQueries(dir)
	if err != nil {
		return nil, spec.DepsDecl{}, err
	}
	var queries []string
	for _, q := range triggers {
		if q.ShouldTrigger {
			queries = append(queries, q.Query)
		}
	}
	var deps spec.DepsDecl
	if meta.Spec != nil {
		deps = meta.Spec.Deps
	}
	return queries, deps, nil
}

// installFolder names the folder a member is installed under: its name with
// the prefix separator replaced, and a number added when another skill holds
// that folder. Two members from two prefixes can share the part after the
// colon, and one folder per skill is what keeps their files apart.
func installFolder(name string, taken map[string]bool) string {
	base := strings.ReplaceAll(name, ":", "-")
	folder := base
	for n := 2; taken[folder]; n++ {
		folder = fmt.Sprintf("%s-%d", base, n)
	}
	taken[folder] = true
	return folder
}
