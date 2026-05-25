package skill

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skael-dev/skael/internal/testutil"
)

func TestMerge(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	source, _ := store.Create(ctx, "superpowers:brainstorming", "", "source", "", json.RawMessage(`{}`))
	target, _ := store.Create(ctx, "brainstorming", "", "target", "", json.RawMessage(`{}`))

	store.CreateVersion(ctx, source.ID, "s/archive.tar.gz", "checksum1", "", json.RawMessage(`{}`), nil, json.RawMessage(`{}`))
	store.CreateVersion(ctx, target.ID, "t/archive.tar.gz", "checksum2", "", json.RawMessage(`{}`), nil, json.RawMessage(`{}`))

	merged, err := store.Merge(ctx, "superpowers:brainstorming", "brainstorming")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if merged.LatestVersion != 2 {
		t.Errorf("latest_version = %d, want 2", merged.LatestVersion)
	}

	gone, _ := store.GetByName(ctx, "superpowers:brainstorming")
	if gone != nil {
		t.Error("source skill should be deleted after merge")
	}

	canonical, _ := store.ResolveAlias(ctx, "superpowers:brainstorming")
	if canonical != "brainstorming" {
		t.Errorf("alias canonical = %q, want %q", canonical, "brainstorming")
	}

	versions, _ := store.ListVersions(ctx, "brainstorming")
	if len(versions) != 2 {
		t.Fatalf("got %d versions, want 2", len(versions))
	}
}
