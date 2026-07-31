package skillimport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

// TestImportSingleSkill_PopulatesSpecFields asserts that importing a skill
// records its spec metadata on the skills row.
//
// The dashboard's tag filter and the tag list endpoint both read skills.tags,
// which is only ever written by UpdateSpecFields. Publishing calls it; import
// used to create the skill and its version directly and skip it, so imported
// skills were invisible to tag filtering and reported no author or licence.
func TestImportSingleSkill_PopulatesSpecFields(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	skillStore := skill.NewStore(pool)
	importStore := NewStore(pool)

	storage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	rootDir := t.TempDir()
	skillDir := filepath.Join(rootDir, "code-review")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	const skillMD = `---
name: code-review
description: Reviews code for correctness and style.
license: Apache-2.0
tags:
  - backend
  - review
metadata:
  author: alice
---

# Code review

Body content.
`
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644))

	ds := DiscoveredSkill{
		Name:        "code-review",
		Description: "Reviews code for correctness and style.",
		Path:        "code-review",
	}
	src := Source{
		Type:      "github",
		Owner:     "acme",
		Repo:      "skills",
		Ref:       "main",
		CommitSHA: "abc123",
	}

	_, created, err := importSingleSkill(ctx, rootDir, ds, src, skillStore, importStore, storage, nil, nil, nil)
	require.NoError(t, err)
	require.True(t, created)

	var tags []string
	var author, license string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT tags, author, license FROM skills WHERE name = $1`, "code-review",
	).Scan(&tags, &author, &license))

	assert.ElementsMatch(t, []string{"backend", "review"}, tags,
		"imported skills must record their frontmatter tags; the dashboard tag filter reads this column")
	assert.Equal(t, "alice", author)
	assert.Equal(t, "Apache-2.0", license)
}

// TestImportSingleSkill_TagsVisibleToTagListing asserts the end-to-end effect:
// an imported skill's tags reach the endpoint that populates the dashboard's
// tag filter.
func TestImportSingleSkill_TagsVisibleToTagListing(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	skillStore := skill.NewStore(pool)
	importStore := NewStore(pool)

	storage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	rootDir := t.TempDir()
	skillDir := filepath.Join(rootDir, "deploy")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	const skillMD = `---
name: deploy
description: Deploys the service to production safely.
tags:
  - ops
---

# Deploy

Body content.
`
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644))

	ds := DiscoveredSkill{Name: "deploy", Description: "Deploys the service.", Path: "deploy"}
	src := Source{Type: "github", Owner: "acme", Repo: "skills", Ref: "main", CommitSHA: "def456"}

	_, _, err = importSingleSkill(ctx, rootDir, ds, src, skillStore, importStore, storage, nil, nil, nil)
	require.NoError(t, err)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM skills WHERE $1 = ANY(tags)`, "ops",
	).Scan(&count))
	assert.Equal(t, 1, count, "an imported skill must be findable by its tag")
}
