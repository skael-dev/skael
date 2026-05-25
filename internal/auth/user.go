package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

type contextKey struct{}

func ContextWithUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, contextKey{}, user)
}

func UserFromContext(ctx context.Context) *User {
	u, _ := ctx.Value(contextKey{}).(*User)
	return u
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("auth.HashPassword: %w", err)
	}
	return string(hash), nil
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func GenerateAPIKey() (fullKey, prefix string, err error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("auth.GenerateAPIKey: %w", err)
	}
	h := hex.EncodeToString(b)
	fullKey = "sk-" + h
	prefix = fullKey[:12]
	return fullKey, prefix, nil
}

func HashAPIKey(key string) (string, error) {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:]), nil
}

func CheckAPIKey(hash, key string) bool {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:]) == hash
}
