package skill_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestStore_CreateAndGet(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	frontmatter := json.RawMessage(`{"tags":["go","testing"]}`)
	created, err := s.Create(ctx, "my-skill", "My Skill", "A test skill", "skill content here", frontmatter)
	require.NoError(t, err)
	require.NotNil(t, created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "my-skill", created.Name)
	require.Equal(t, "My Skill", created.DisplayName)
	require.Equal(t, "A test skill", created.Description)
	require.Equal(t, "skill content here", created.Content)
	require.Equal(t, 0, created.LatestVersion)
	require.JSONEq(t, `{"tags":["go","testing"]}`, string(created.Frontmatter))
	require.False(t, created.CreatedAt.IsZero())
	require.False(t, created.UpdatedAt.IsZero())

	got, err := s.GetByName(ctx, "my-skill")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, "my-skill", got.Name)
	require.Equal(t, "My Skill", got.DisplayName)
	require.Equal(t, "A test skill", got.Description)
}

func TestStore_GetByName_NotFound(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	got, err := s.GetByName(ctx, "nonexistent-skill")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestStore_List(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	_, err := s.Create(ctx, "skill-alpha", "Skill Alpha", "First skill", "", json.RawMessage(`{}`))
	require.NoError(t, err)

	_, err = s.Create(ctx, "skill-beta", "Skill Beta", "Second skill", "", json.RawMessage(`{}`))
	require.NoError(t, err)

	skills, total, err := s.List(ctx, 10, 0)
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, skills, 2)
}

func TestStore_Delete(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	_, err := s.Create(ctx, "deletable-skill", "", "A skill to delete", "", json.RawMessage(`{}`))
	require.NoError(t, err)

	err = s.Delete(ctx, "deletable-skill")
	require.NoError(t, err)

	got, err := s.GetByName(ctx, "deletable-skill")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestStore_CreateVersion(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	sk, err := s.Create(ctx, "versioned-skill", "Versioned Skill", "A skill with versions", "", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.Equal(t, 0, sk.LatestVersion)

	manifest := []skill.FileEntry{
		{Path: "skill.md", Size: 1024},
		{Path: "README.md", Size: 256},
	}
	scanResult := json.RawMessage(`{"clean":true}`)
	ver, err := s.CreateVersion(ctx, sk.ID, "/archives/versioned-skill-v1.tar.gz", "abc123checksum", "initial release", json.RawMessage(`{}`), manifest, scanResult)
	require.NoError(t, err)
	require.NotNil(t, ver)
	require.Equal(t, 1, ver.Version)
	require.Equal(t, sk.ID, ver.SkillID)
	require.Equal(t, "abc123checksum", ver.Checksum)
	require.Equal(t, "initial release", ver.Changelog)
	require.Len(t, ver.FileManifest, 2)

	// Verify latest_version was incremented on the skill.
	updated, err := s.GetByName(ctx, "versioned-skill")
	require.NoError(t, err)
	require.Equal(t, 1, updated.LatestVersion)
}

func TestStore_UpdateContent(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	_, err := s.Create(ctx, "update-skill", "Update Skill", "original description", "original content", json.RawMessage(`{}`))
	require.NoError(t, err)

	newFrontmatter := json.RawMessage(`{"tags":["updated"]}`)
	err = s.UpdateContent(ctx, "update-skill", "new description", "new content body", newFrontmatter)
	require.NoError(t, err)

	got, err := s.GetByName(ctx, "update-skill")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "new description", got.Description)
	require.Equal(t, "new content body", got.Content)
	require.JSONEq(t, `{"tags":["updated"]}`, string(got.Frontmatter))
}

func TestStore_GetVersion(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	sk, err := s.Create(ctx, "getver-skill", "GetVersion Skill", "test skill", "", json.RawMessage(`{}`))
	require.NoError(t, err)

	manifest := []skill.FileEntry{{Path: "SKILL.md", Size: 512}}
	scanResult := json.RawMessage(`{"status":"clean"}`)
	created, err := s.CreateVersion(ctx, sk.ID, "/archives/getver-v1.tar.gz", "deadbeef1234", "first release", json.RawMessage(`{}`), manifest, scanResult)
	require.NoError(t, err)
	require.Equal(t, 1, created.Version)

	ver, err := s.GetVersion(ctx, "getver-skill", 1)
	require.NoError(t, err)
	require.NotNil(t, ver)
	require.Equal(t, 1, ver.Version)
	require.Equal(t, sk.ID, ver.SkillID)
	require.Equal(t, "deadbeef1234", ver.Checksum)
	require.Equal(t, "first release", ver.Changelog)
	require.Equal(t, "/archives/getver-v1.tar.gz", ver.ArchivePath)
	require.Len(t, ver.FileManifest, 1)
	require.Equal(t, "SKILL.md", ver.FileManifest[0].Path)
}

func TestStore_ListVersions(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	sk, err := s.Create(ctx, "multi-version-skill", "Multi Version", "Skill with many versions", "", json.RawMessage(`{}`))
	require.NoError(t, err)

	manifest := []skill.FileEntry{{Path: "skill.md", Size: 512}}

	_, err = s.CreateVersion(ctx, sk.ID, "/archives/v1.tar.gz", "checksum1", "version 1", json.RawMessage(`{}`), manifest, json.RawMessage(`{}`))
	require.NoError(t, err)

	_, err = s.CreateVersion(ctx, sk.ID, "/archives/v2.tar.gz", "checksum2", "version 2", json.RawMessage(`{}`), manifest, json.RawMessage(`{}`))
	require.NoError(t, err)

	versions, err := s.ListVersions(ctx, "multi-version-skill")
	require.NoError(t, err)
	require.Len(t, versions, 2)

	// Results should be ordered by version DESC: v2 first, v1 second.
	require.Equal(t, 2, versions[0].Version)
	require.Equal(t, 1, versions[1].Version)
}
