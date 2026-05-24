package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/testutil"
)

// createTestUser is a helper that creates a user and returns its ID.
func createTestUser(t *testing.T, store *auth.UserStore, email string) string {
	t.Helper()
	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u, err := store.Create(context.Background(), email, "Test User", hash)
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return u.ID
}

func TestKeyStore_CreateAndGetByPrefix(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	userStore := auth.NewUserStore(pool)
	keyStore := auth.NewKeyStore(pool)

	userID := createTestUser(t, userStore, "keytest@example.com")

	fullKey, prefix, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("generate api key: %v", err)
	}
	keyHash, err := auth.HashAPIKey(fullKey)
	if err != nil {
		t.Fatalf("hash api key: %v", err)
	}

	created, err := keyStore.Create(ctx, userID, "my-key", prefix, keyHash)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if created.ID == "" {
		t.Error("expected non-empty ID")
	}
	if created.UserID != userID {
		t.Errorf("UserID = %q, want %q", created.UserID, userID)
	}
	if created.KeyPrefix != prefix {
		t.Errorf("KeyPrefix = %q, want %q", created.KeyPrefix, prefix)
	}
	if created.KeyHash != keyHash {
		t.Error("KeyHash mismatch")
	}
	if created.LastUsedAt != nil {
		t.Error("expected nil LastUsedAt on fresh key")
	}

	got, err := keyStore.GetByPrefix(ctx, prefix)
	if err != nil {
		t.Fatalf("get by prefix: %v", err)
	}
	if got == nil {
		t.Fatal("expected key, got nil")
	}
	if got.ID != created.ID {
		t.Errorf("ID = %q, want %q", got.ID, created.ID)
	}
	// Verify the hash matches the original key via bcrypt.
	if !auth.CheckAPIKey(got.KeyHash, fullKey) {
		t.Error("bcrypt check failed: hash does not match original key")
	}
}

func TestKeyStore_ListByUser(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	userStore := auth.NewUserStore(pool)
	keyStore := auth.NewKeyStore(pool)

	userID := createTestUser(t, userStore, "listkeys@example.com")

	for _, name := range []string{"key-one", "key-two"} {
		_, prefix, err := auth.GenerateAPIKey()
		if err != nil {
			t.Fatalf("generate api key: %v", err)
		}
		_, err = keyStore.Create(ctx, userID, name, prefix, "fakehash")
		if err != nil {
			t.Fatalf("create key %q: %v", name, err)
		}
	}

	keys, err := keyStore.ListByUser(ctx, userID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("len(keys) = %d, want 2", len(keys))
	}
}

func TestKeyStore_Delete(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	userStore := auth.NewUserStore(pool)
	keyStore := auth.NewKeyStore(pool)

	userID := createTestUser(t, userStore, "delkey@example.com")

	_, prefix, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("generate api key: %v", err)
	}
	created, err := keyStore.Create(ctx, userID, "to-delete", prefix, "fakehash")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	if err := keyStore.Delete(ctx, created.ID, userID); err != nil {
		t.Fatalf("delete key: %v", err)
	}

	keys, err := keyStore.ListByUser(ctx, userID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected 0 keys after delete, got %d", len(keys))
	}
}

func TestKeyStore_GetByPrefix_NotFound(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	keyStore := auth.NewKeyStore(pool)

	got, err := keyStore.GetByPrefix(ctx, "sk-xxxx")
	if err != nil {
		t.Fatalf("get by prefix: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestKeyStore_UpdateLastUsed(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	userStore := auth.NewUserStore(pool)
	keyStore := auth.NewKeyStore(pool)

	userID := createTestUser(t, userStore, "lastused@example.com")

	_, prefix, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("generate api key: %v", err)
	}
	created, err := keyStore.Create(ctx, userID, "tracked", prefix, "fakehash")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if created.LastUsedAt != nil {
		t.Error("expected nil LastUsedAt before update")
	}

	before := time.Now().UTC().Add(-time.Second)
	keyStore.UpdateLastUsed(ctx, created.ID)

	got, err := keyStore.GetByPrefix(ctx, prefix)
	if err != nil {
		t.Fatalf("get by prefix: %v", err)
	}
	if got == nil {
		t.Fatal("expected key, got nil")
	}
	if got.LastUsedAt == nil {
		t.Fatal("expected non-nil LastUsedAt after update")
	}
	if got.LastUsedAt.UTC().Before(before) {
		t.Errorf("LastUsedAt %v is before expected time %v", got.LastUsedAt, before)
	}
}
