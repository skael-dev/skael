package skill_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/skill"
)

func TestDiffAgainstServedShowsSkillMDChanges(t *testing.T) {
	_, store, ctx := gateFixture(t)

	sk := newGateSkill(t, store, ctx, "diff-skillmd")
	v1, err := store.CreateVersion(ctx, sk.ID, "p1", "c1", "", "d", "line one\nline two\n",
		json.RawMessage(`{}`), []skill.FileEntry{{Path: "SKILL.md", Size: 10}},
		json.RawMessage(`{}`), "t", gate.Decision{Outcome: gate.Allow})
	require.NoError(t, err)
	require.Equal(t, 1, v1.Version)

	v2, err := store.CreateVersion(ctx, sk.ID, "p2", "c2", "", "d", "line one\nline TWO\n",
		json.RawMessage(`{}`), []skill.FileEntry{{Path: "SKILL.md", Size: 10}},
		json.RawMessage(`{}`), "t", gate.Decision{Outcome: gate.NeedsReview, Reasons: []gate.Reason{{Rule: "x", Class: "execution"}}})
	require.NoError(t, err)
	require.Equal(t, 2, v2.Version)

	diff, err := store.DiffAgainstServed(ctx, "diff-skillmd", v2.Version)
	require.NoError(t, err)
	require.NotNil(t, diff)
	assert.Equal(t, 1, diff.Against)
	assert.Contains(t, diff.SkillMD, "-line two")
	assert.Contains(t, diff.SkillMD, "+line TWO")
}

// A non-SKILL.md addition must be reported, because that is exactly when an
// owner should stop and read the bundle properly.
func TestDiffFlagsNonSkillMDFiles(t *testing.T) {
	_, store, ctx := gateFixture(t)

	sk := newGateSkill(t, store, ctx, "diff-files")
	v1, err := store.CreateVersion(ctx, sk.ID, "p1", "c1", "", "d", "content\n",
		json.RawMessage(`{}`), []skill.FileEntry{{Path: "SKILL.md", Size: 8}},
		json.RawMessage(`{}`), "t", gate.Decision{Outcome: gate.Allow})
	require.NoError(t, err)
	require.Equal(t, 1, v1.Version)

	v2, err := store.CreateVersion(ctx, sk.ID, "p2", "c2", "", "d", "content\n",
		json.RawMessage(`{}`),
		[]skill.FileEntry{{Path: "SKILL.md", Size: 8}, {Path: "scripts/setup.sh", Size: 42}},
		json.RawMessage(`{}`), "t", gate.Decision{Outcome: gate.NeedsReview, Reasons: []gate.Reason{{Rule: "x", Class: "execution"}}})
	require.NoError(t, err)
	require.Equal(t, 2, v2.Version)

	diff, err := store.DiffAgainstServed(ctx, "diff-files", v2.Version)
	require.NoError(t, err)
	require.NotNil(t, diff)
	assert.Equal(t, 1, diff.Against)
	assert.Contains(t, diff.Files, skill.FileChange{Path: "scripts/setup.sh", Status: "added"})
}

// First publish has nothing to diff against and must say so rather than
// erroring or rendering the whole file as additions with a phantom baseline.
func TestDiffOnFirstVersionHasNoBaseline(t *testing.T) {
	_, store, ctx := gateFixture(t)

	sk := newGateSkill(t, store, ctx, "diff-first")
	v1, err := store.CreateVersion(ctx, sk.ID, "p1", "c1", "", "d", "content\n",
		json.RawMessage(`{}`),
		[]skill.FileEntry{{Path: "SKILL.md", Size: 8}, {Path: "scripts/setup.sh", Size: 42}},
		json.RawMessage(`{}`), "t", gate.Decision{Outcome: gate.NeedsReview, Reasons: []gate.Reason{{Rule: "x", Class: "execution"}}})
	require.NoError(t, err)
	require.Equal(t, 1, v1.Version)

	diff, err := store.DiffAgainstServed(ctx, "diff-first", v1.Version)
	require.NoError(t, err)
	require.NotNil(t, diff)
	assert.Equal(t, 0, diff.Against)
	require.Len(t, diff.Files, 2)
	for _, f := range diff.Files {
		assert.Equal(t, "added", f.Status)
	}
}
