package auth_test

import (
	"context"
	"testing"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestUserStore_CreateAndGet(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := auth.NewUserStore(pool)

	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	created, err := store.Create(ctx, "alice@example.com", "Alice", hash)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if created.ID == "" {
		t.Error("expected non-empty ID")
	}
	if created.Email != "alice@example.com" {
		t.Errorf("email = %q, want %q", created.Email, "alice@example.com")
	}
	if created.Name != "Alice" {
		t.Errorf("name = %q, want %q", created.Name, "Alice")
	}
	if created.PasswordHash != hash {
		t.Error("password hash mismatch")
	}
	if created.Role != "admin" {
		t.Errorf("role = %q, want %q", created.Role, "admin")
	}
	if created.CreatedAt.IsZero() {
		t.Error("expected non-zero created_at")
	}

	got, err := store.GetByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if got == nil {
		t.Fatal("expected user, got nil")
	}
	if got.ID != created.ID {
		t.Errorf("ID = %q, want %q", got.ID, created.ID)
	}
	if got.Email != created.Email {
		t.Errorf("email = %q, want %q", got.Email, created.Email)
	}
	if got.Name != created.Name {
		t.Errorf("name = %q, want %q", got.Name, created.Name)
	}
	if got.Role != created.Role {
		t.Errorf("role = %q, want %q", got.Role, created.Role)
	}
}

func TestUserStore_CreateDuplicateEmail(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := auth.NewUserStore(pool)

	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	_, err = store.Create(ctx, "dup@example.com", "First", hash)
	if err != nil {
		t.Fatalf("create first user: %v", err)
	}

	_, err = store.Create(ctx, "dup@example.com", "Second", hash)
	if err == nil {
		t.Fatal("expected error for duplicate email, got nil")
	}
}

func TestUserStore_Count(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := auth.NewUserStore(pool)

	n, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}

	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	_, err = store.Create(ctx, "count@example.com", "Counter", hash)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	n, err = store.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
}

func TestUserStore_GetByID(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := auth.NewUserStore(pool)

	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	created, err := store.Create(ctx, "byid@example.com", "ByID", hash)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	got, err := store.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got == nil {
		t.Fatal("expected user, got nil")
	}
	if got.ID != created.ID {
		t.Errorf("ID = %q, want %q", got.ID, created.ID)
	}
	if got.Email != created.Email {
		t.Errorf("email = %q, want %q", got.Email, created.Email)
	}
	if got.Name != created.Name {
		t.Errorf("name = %q, want %q", got.Name, created.Name)
	}
	if got.Role != created.Role {
		t.Errorf("role = %q, want %q", got.Role, created.Role)
	}
}

func TestUserStore_GetByEmail_NotFound(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := auth.NewUserStore(pool)

	got, err := store.GetByEmail(ctx, "nobody@example.com")
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestUserStore_CreateWithRole(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := auth.NewUserStore(pool)

	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	created, err := store.CreateWithRole(ctx, "owner@example.com", "Owner", hash, "owner")
	if err != nil {
		t.Fatalf("create with role: %v", err)
	}
	if created.Role != "owner" {
		t.Errorf("role = %q, want %q", created.Role, "owner")
	}
	if created.Email != "owner@example.com" {
		t.Errorf("email = %q, want %q", created.Email, "owner@example.com")
	}
}
