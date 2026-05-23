package sync_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skael-dev/skael/internal/skill"
	syncs "github.com/skael-dev/skael/internal/sync"
	"github.com/skael-dev/skael/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestManifest_ReflectsState(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	skillStore := skill.NewStore(pool)
	syncStore := syncs.NewStore(pool)

	// Create two skills with one version each.
	sk1, err := skillStore.Create(ctx, "alpha-skill", "Alpha Skill", "First skill", "content alpha", json.RawMessage(`{}`))
	require.NoError(t, err)

	manifest1 := []skill.FileEntry{{Path: "skill.md", Size: 512}}
	_, err = skillStore.CreateVersion(ctx, sk1.ID, "/archives/alpha-v1.tar.gz", "checksumAlpha1", "initial alpha", json.RawMessage(`{}`), manifest1, json.RawMessage(`{}`))
	require.NoError(t, err)

	sk2, err := skillStore.Create(ctx, "beta-skill", "Beta Skill", "Second skill", "content beta", json.RawMessage(`{}`))
	require.NoError(t, err)

	manifest2 := []skill.FileEntry{{Path: "skill.md", Size: 256}}
	_, err = skillStore.CreateVersion(ctx, sk2.ID, "/archives/beta-v1.tar.gz", "checksumBeta1", "initial beta", json.RawMessage(`{}`), manifest2, json.RawMessage(`{}`))
	require.NoError(t, err)

	// GetManifest should return both entries.
	entries, err := syncStore.GetManifest(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// Results are ordered by name: alpha-skill first, then beta-skill.
	require.Equal(t, "alpha-skill", entries[0].Name)
	require.Greater(t, entries[0].Version, 0)
	require.NotEmpty(t, entries[0].Checksum)

	require.Equal(t, "beta-skill", entries[1].Name)
	require.Greater(t, entries[1].Version, 0)
	require.NotEmpty(t, entries[1].Checksum)
}
