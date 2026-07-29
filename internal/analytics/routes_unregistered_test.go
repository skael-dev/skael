package analytics

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestGetUnregisteredSkills(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	store := NewStore(pool)
	skillStore := skill.NewStore(pool)

	// Create a registered skill.
	if _, err := skillStore.Create(ctx, "registered-skill", "", "exists", "", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Insert events for registered + unregistered + dismissed skills.
	if err := store.Insert(ctx, Event{SkillName: "registered-skill", Agent: "claude-code", DeveloperHash: "dev1"}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := store.Insert(ctx, Event{SkillName: "shadow-skill", Agent: "claude-code", DeveloperHash: "dev1"}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := store.Insert(ctx, Event{SkillName: "shadow-skill", Agent: "opencode", DeveloperHash: "dev2"}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := store.Insert(ctx, Event{SkillName: "dismissed-one", Agent: "claude-code", DeveloperHash: "dev1"}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Dismiss one.
	if err := store.DismissSkill(ctx, "dismissed-one"); err != nil {
		t.Fatalf("DismissSkill: %v", err)
	}

	results, err := store.GetUnregisteredSkills(ctx, 30)
	if err != nil {
		t.Fatalf("GetUnregisteredSkills: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (only shadow-skill)", len(results))
	}
	if results[0].Name != "shadow-skill" {
		t.Errorf("name = %q, want %q", results[0].Name, "shadow-skill")
	}
	if results[0].Activations != 2 {
		t.Errorf("activations = %d, want 2", results[0].Activations)
	}
	if results[0].UniqueDevs != 2 {
		t.Errorf("unique_devs = %d, want 2", results[0].UniqueDevs)
	}
}

func TestDismissSkill_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	if err := store.DismissSkill(ctx, "test-dismiss"); err != nil {
		t.Fatalf("first dismiss: %v", err)
	}
	if err := store.DismissSkill(ctx, "test-dismiss"); err != nil {
		t.Fatalf("second dismiss (should be idempotent): %v", err)
	}
}
