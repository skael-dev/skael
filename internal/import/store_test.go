package skillimport

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestStore_UpsertAndGet(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	skillStore := skill.NewStore(pool)
	sk, err := skillStore.Create(ctx, "test-import", "", "test skill", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}

	store := NewStore(pool)

	src := ImportSource{
		SkillID:    sk.ID,
		SourceType: "github",
		SourceURL:  "https://github.com/anthropics/skills",
		SourcePath: "skills/test-import",
		SourceRef:  "main",
		CommitSHA:  "abc123",
	}
	err = store.Upsert(ctx, src)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := store.GetBySkillID(ctx, sk.ID)
	if err != nil {
		t.Fatalf("GetBySkillID: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.SourceURL != src.SourceURL {
		t.Errorf("SourceURL = %q, want %q", got.SourceURL, src.SourceURL)
	}
	if got.CommitSHA != "abc123" {
		t.Errorf("CommitSHA = %q, want %q", got.CommitSHA, "abc123")
	}
}

func TestStore_UpsertUpdatesExisting(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	skillStore := skill.NewStore(pool)
	sk, err := skillStore.Create(ctx, "test-reimport", "", "test", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}

	store := NewStore(pool)

	store.Upsert(ctx, ImportSource{SkillID: sk.ID, SourceType: "github", CommitSHA: "aaa"})
	store.Upsert(ctx, ImportSource{SkillID: sk.ID, SourceType: "github", CommitSHA: "bbb"})

	got, _ := store.GetBySkillID(ctx, sk.ID)
	if got.CommitSHA != "bbb" {
		t.Errorf("CommitSHA = %q, want %q (upsert should update)", got.CommitSHA, "bbb")
	}
}

func TestStore_ListAll(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	skillStore := skill.NewStore(pool)
	sk1, _ := skillStore.Create(ctx, "list-a", "", "a", "", json.RawMessage(`{}`))
	sk2, _ := skillStore.Create(ctx, "list-b", "", "b", "", json.RawMessage(`{}`))

	store := NewStore(pool)
	store.Upsert(ctx, ImportSource{SkillID: sk1.ID, SourceType: "github", SourceURL: "https://github.com/a/a"})
	store.Upsert(ctx, ImportSource{SkillID: sk2.ID, SourceType: "local", SourceURL: ""})

	all, err := store.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d, want 2", len(all))
	}
}
