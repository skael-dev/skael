package skill_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"golang.org/x/sync/errgroup"

	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestStore_CreateAndGet(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
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
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	got, err := s.GetByName(ctx, "nonexistent-skill")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestStore_List(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	_, err := s.Create(ctx, "skill-alpha", "Skill Alpha", "First skill", "", json.RawMessage(`{}`))
	require.NoError(t, err)

	_, err = s.Create(ctx, "skill-beta", "Skill Beta", "Second skill", "", json.RawMessage(`{}`))
	require.NoError(t, err)

	skills, total, err := s.List(ctx, skill.ListOptions{Limit: 10, Offset: 0})
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, skills, 2)
}

func TestStore_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
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
	if testing.Short() {
		t.Skip("requires database")
	}
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
	ver, err := s.CreateVersion(ctx, sk.ID, "/archives/versioned-skill-v1.tar.gz", "abc123checksum", "initial release", "", "", json.RawMessage(`{}`), manifest, scanResult, "test@example.com", allowDecision())
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

func TestStore_GetVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	sk, err := s.Create(ctx, "getver-skill", "GetVersion Skill", "test skill", "", json.RawMessage(`{}`))
	require.NoError(t, err)

	manifest := []skill.FileEntry{{Path: "SKILL.md", Size: 512}}
	scanResult := json.RawMessage(`{"status":"clean"}`)
	created, err := s.CreateVersion(ctx, sk.ID, "/archives/getver-v1.tar.gz", "deadbeef1234", "first release", "", "", json.RawMessage(`{}`), manifest, scanResult, "test@example.com", allowDecision())
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

func TestStore_CreateVersion_UpdatesSkillMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	store := skill.NewStore(pool)
	ctx := context.Background()

	sk, err := store.Create(ctx, "meta-skill", "", "old desc", "old content", json.RawMessage(`{}`))
	require.NoError(t, err)

	_, err = store.CreateVersion(ctx, sk.ID, "meta-skill/abc.tar.gz", "abc", "",
		"new desc", "new content",
		json.RawMessage(`{"description":"new desc"}`), nil, json.RawMessage(`{}`), "test@example.com", allowDecision())
	require.NoError(t, err)

	got, err := store.GetByName(ctx, "meta-skill")
	require.NoError(t, err)
	require.Equal(t, "new desc", got.Description)
	require.Equal(t, "new content", got.Content)
	require.Equal(t, 1, got.LatestVersion)
}

func TestStore_List_FilterByAuthor(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	sk1, err := s.Create(ctx, "skill-a", "", "first", "", json.RawMessage(`{}`))
	require.NoError(t, err)
	err = s.UpdateSpecFields(ctx, sk1.Name, "alice", "", "", "full", "", []string{})
	require.NoError(t, err)

	sk2, err := s.Create(ctx, "skill-b", "", "second", "", json.RawMessage(`{}`))
	require.NoError(t, err)
	err = s.UpdateSpecFields(ctx, sk2.Name, "bob", "", "", "full", "", []string{})
	require.NoError(t, err)

	skills, total, err := s.List(ctx, skill.ListOptions{Limit: 10, Author: "alice"})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, skills, 1)
	require.Equal(t, "skill-a", skills[0].Name)
}

func TestStore_List_FilterByTag(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	sk1, err := s.Create(ctx, "skill-tagged", "", "tagged", "", json.RawMessage(`{}`))
	require.NoError(t, err)
	err = s.UpdateSpecFields(ctx, sk1.Name, "", "", "", "", "", []string{"go", "testing"})
	require.NoError(t, err)

	_, err = s.Create(ctx, "skill-untagged", "", "untagged", "", json.RawMessage(`{}`))
	require.NoError(t, err)

	skills, total, err := s.List(ctx, skill.ListOptions{Limit: 10, Tag: "go"})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, skills, 1)
	require.Equal(t, "skill-tagged", skills[0].Name)
}

func TestStore_List_FilterByLicense(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	sk1, err := s.Create(ctx, "skill-mit", "", "mit-licensed", "", json.RawMessage(`{}`))
	require.NoError(t, err)
	err = s.UpdateSpecFields(ctx, sk1.Name, "", "MIT", "", "", "", []string{})
	require.NoError(t, err)

	sk2, err := s.Create(ctx, "skill-apache", "", "apache-licensed", "", json.RawMessage(`{}`))
	require.NoError(t, err)
	err = s.UpdateSpecFields(ctx, sk2.Name, "", "Apache-2.0", "", "", "", []string{})
	require.NoError(t, err)

	skills, total, err := s.List(ctx, skill.ListOptions{Limit: 10, License: "MIT"})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, skills, 1)
	require.Equal(t, "skill-mit", skills[0].Name)
}

func TestStore_ListVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	sk, err := s.Create(ctx, "multi-version-skill", "Multi Version", "Skill with many versions", "", json.RawMessage(`{}`))
	require.NoError(t, err)

	manifest := []skill.FileEntry{{Path: "skill.md", Size: 512}}

	_, err = s.CreateVersion(ctx, sk.ID, "/archives/v1.tar.gz", "checksum1", "version 1", "", "", json.RawMessage(`{}`), manifest, json.RawMessage(`{}`), "test@example.com", allowDecision())
	require.NoError(t, err)

	_, err = s.CreateVersion(ctx, sk.ID, "/archives/v2.tar.gz", "checksum2", "version 2", "", "", json.RawMessage(`{}`), manifest, json.RawMessage(`{}`), "test@example.com", allowDecision())
	require.NoError(t, err)

	versions, err := s.ListVersions(ctx, "multi-version-skill")
	require.NoError(t, err)
	require.Len(t, versions, 2)

	// Results should be ordered by version DESC: v2 first, v1 second.
	require.Equal(t, 2, versions[0].Version)
	require.Equal(t, 1, versions[1].Version)
}

// allowDecision is the clean-scan gate decision: nothing to hold on.
func allowDecision() gate.Decision {
	return gate.Decision{Outcome: gate.Allow, Reasons: []gate.Reason{}}
}

func heldDecision(reasons ...gate.Reason) gate.Decision {
	if reasons == nil {
		reasons = []gate.Reason{}
	}
	return gate.Decision{Outcome: gate.NeedsReview, Reasons: reasons}
}

// mustSkill creates a skill row and fails the test if it cannot.
func mustSkill(t *testing.T, s *skill.Store, name string) *skill.Skill {
	t.Helper()
	sk, err := s.Create(context.Background(), name, name, "", "", json.RawMessage(`{}`))
	require.NoError(t, err)
	return sk
}

// A held version exists and is numbered, but skills.latest_version must not
// point at it — that pointer is what every reader serves from.
func TestCreateVersionHeldDoesNotAdvanceLatest(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()
	sk := mustSkill(t, s, "held-skill")

	v1, err := s.CreateVersion(ctx, sk.ID, "p1", "c1", "", "d", "c",
		json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "tester", allowDecision())
	require.NoError(t, err)
	require.Equal(t, 1, v1.Version)
	require.Equal(t, "released", v1.GateState)

	v2, err := s.CreateVersion(ctx, sk.ID, "p2", "c2", "", "d", "c",
		json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "tester",
		heldDecision(gate.Reason{Rule: "x", Class: "execution"}))
	require.NoError(t, err)
	require.Equal(t, 2, v2.Version, "a held version still gets the next version number")
	require.Equal(t, "needs_review", v2.GateState)

	got, err := s.GetByName(ctx, "held-skill")
	require.NoError(t, err)
	require.Equal(t, 1, got.LatestVersion,
		"the latest pointer must not advance to a held version; this is the whole invariant")
}

func TestCreateVersionRecordsTheDecision(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()
	sk := mustSkill(t, s, "decision-skill")

	d := heldDecision(gate.Reason{
		Rule: "curl-pipe-sh", Class: "execution", Severity: "high",
		File: "install.sh", Line: 4, Clears: "an evaluation",
	})
	v, err := s.CreateVersion(ctx, sk.ID, "p", "c", "", "d", "c",
		json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "tester", d)
	require.NoError(t, err)

	var back gate.Decision
	require.NoError(t, json.Unmarshal(v.GateDecision, &back))
	require.Equal(t, d, back, "the decision must survive the round trip intact; the review screen renders it")

	// And again after a read, not only from the INSERT ... RETURNING row.
	read, err := s.GetVersion(ctx, "decision-skill", 1)
	require.NoError(t, err)
	require.Equal(t, "needs_review", read.GateState)
	var reread gate.Decision
	require.NoError(t, json.Unmarshal(read.GateDecision, &reread))
	require.Equal(t, d, reread)
}

// The version number comes from MAX(version), not from the latest pointer:
// two consecutive held publishes must not both try to be version 1.
func TestCreateVersionNumbersFromMaxNotFromPointer(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()
	sk := mustSkill(t, s, "numbering-skill")
	held := heldDecision()

	_, err := s.CreateVersion(ctx, sk.ID, "p1", "c1", "", "d", "c", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "t", held)
	require.NoError(t, err)
	v2, err := s.CreateVersion(ctx, sk.ID, "p2", "c2", "", "d", "c", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "t", held)
	require.NoError(t, err)
	require.Equal(t, 2, v2.Version, "two consecutive held publishes must not collide on version 1")

	v3, err := s.CreateVersion(ctx, sk.ID, "p3", "c3", "", "d", "c", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "t", allowDecision())
	require.NoError(t, err)
	require.Equal(t, 3, v3.Version)

	got, err := s.GetByName(ctx, "numbering-skill")
	require.NoError(t, err)
	require.Equal(t, 3, got.LatestVersion, "a released publish after two held ones advances the pointer to itself")
}

// The UPDATE skills statement in CreateVersion looks vestigial now that it no
// longer returns the version number. It is not: it takes the row lock that
// serialises concurrent publishes of the same skill. Delete it and this test
// goes red on UNIQUE(skill_id, version).
func TestCreateVersionConcurrentPublishesDoNotCollide(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()
	sk := mustSkill(t, s, "concurrent-skill")

	const n = 8
	var mu sync.Mutex
	seen := map[int]bool{}
	g, gctx := errgroup.WithContext(ctx)
	for i := 0; i < n; i++ {
		i := i
		g.Go(func() error {
			v, err := s.CreateVersion(gctx, sk.ID,
				fmt.Sprintf("p%d", i), fmt.Sprintf("c%d", i), "", "d", "c",
				json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "t", heldDecision())
			if err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			if seen[v.Version] {
				return fmt.Errorf("duplicate version %d", v.Version)
			}
			seen[v.Version] = true
			return nil
		})
	}
	require.NoError(t, g.Wait())

	for want := 1; want <= n; want++ {
		require.True(t, seen[want], "version %d missing; got %v", want, seen)
	}
	require.Len(t, seen, n)

	// All eight were held, so the pointer stayed put.
	got, err := s.GetByName(ctx, "concurrent-skill")
	require.NoError(t, err)
	require.Equal(t, 0, got.LatestVersion)
}
