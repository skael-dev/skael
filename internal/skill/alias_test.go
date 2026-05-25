package skill

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skael-dev/skael/internal/testutil"
)

func TestAlias_CreateAndList(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	store.Create(ctx, "brainstorming", "", "test", "", json.RawMessage(`{}`))

	if err := store.CreateAlias(ctx, "superpowers:brainstorming", "brainstorming"); err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}

	aliases, err := store.ListAliases(ctx, "brainstorming")
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("got %d aliases, want 1", len(aliases))
	}
	if aliases[0].Alias != "superpowers:brainstorming" {
		t.Errorf("alias = %q, want %q", aliases[0].Alias, "superpowers:brainstorming")
	}
}

func TestAlias_Resolve(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	store.Create(ctx, "brainstorming", "", "test", "", json.RawMessage(`{}`))
	store.CreateAlias(ctx, "superpowers:brainstorming", "brainstorming")

	canonical, err := store.ResolveAlias(ctx, "superpowers:brainstorming")
	if err != nil {
		t.Fatalf("ResolveAlias: %v", err)
	}
	if canonical != "brainstorming" {
		t.Errorf("canonical = %q, want %q", canonical, "brainstorming")
	}

	notFound, err := store.ResolveAlias(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("ResolveAlias not found: %v", err)
	}
	if notFound != "" {
		t.Errorf("expected empty string for nonexistent, got %q", notFound)
	}
}

func TestAlias_Delete(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	store.Create(ctx, "brainstorming", "", "test", "", json.RawMessage(`{}`))
	store.CreateAlias(ctx, "superpowers:brainstorming", "brainstorming")
	store.DeleteAlias(ctx, "superpowers:brainstorming")

	aliases, _ := store.ListAliases(ctx, "brainstorming")
	if len(aliases) != 0 {
		t.Errorf("got %d aliases after delete, want 0", len(aliases))
	}
}

func TestAlias_Idempotent(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	store.Create(ctx, "brainstorming", "", "test", "", json.RawMessage(`{}`))

	if err := store.CreateAlias(ctx, "superpowers:brainstorming", "brainstorming"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := store.CreateAlias(ctx, "superpowers:brainstorming", "brainstorming"); err != nil {
		t.Fatalf("second (should be idempotent): %v", err)
	}
}
