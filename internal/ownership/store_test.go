package ownership_test

import (
	"context"
	"testing"

	"github.com/skael-dev/skael/internal/ownership"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestStoreUpsertReplacesMembers(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	store := ownership.NewStore(pool)

	var alice, bob, carol string
	for _, p := range []struct {
		email string
		into  *string
	}{{"alice@x.com", &alice}, {"bob@x.com", &bob}, {"carol@x.com", &carol}} {
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (email, name, password_hash, role)
			 VALUES ($1, $1, 'x', 'member') RETURNING id`, p.email).Scan(p.into); err != nil {
			t.Fatalf("seed %s: %v", p.email, err)
		}
	}

	r, err := store.Upsert(ctx, "payments:*", []string{alice, bob}, alice)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if len(r.Members) != 2 {
		t.Fatalf("members = %v, want 2", r.Members)
	}

	// Upsert REPLACES the member list; it does not merge. `skael owners set`
	// promises exactly this.
	r2, err := store.Upsert(ctx, "payments:*", []string{carol}, alice)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if r2.ID != r.ID {
		t.Fatalf("upsert created a second rule for the same pattern: %s vs %s", r2.ID, r.ID)
	}
	if len(r2.Members) != 1 || r2.Members[0] != carol {
		t.Fatalf("members = %v, want exactly [%s]", r2.Members, carol)
	}
}

func TestStoreListAndDelete(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	store := ownership.NewStore(pool)

	var alice string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, name, password_hash, role)
		 VALUES ('a@x.com', 'a', 'x', 'member') RETURNING id`).Scan(&alice); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := store.Upsert(ctx, "payments:*", []string{alice}, alice); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	r, err := store.Upsert(ctx, "billing:*", []string{alice}, alice)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rules, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("list returned %d rules, want 2", len(rules))
	}

	ok, err := store.Delete(ctx, r.ID)
	if err != nil || !ok {
		t.Fatalf("delete = (%v, %v), want (true, nil)", ok, err)
	}
	ok, err = store.Delete(ctx, r.ID)
	if err != nil || ok {
		t.Fatalf("second delete = (%v, %v), want (false, nil)", ok, err)
	}
}

// A rule with no members is meaningless — it would resolve a name to an owned
// state with nobody able to review it, which is strictly worse than unowned.
func TestStoreRejectsEmptyMemberList(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	store := ownership.NewStore(pool)

	if _, err := store.Upsert(ctx, "payments:*", nil, ""); err == nil {
		t.Fatal("Upsert with no members succeeded; it must be rejected")
	}
}
