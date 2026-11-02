package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	Kind  string `json:"-"`
}

// keyPrefixLen is the stored, indexed start of an sk- key.
const keyPrefixLen = 12

type contextKey struct{}

func ContextWithUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, contextKey{}, user)
}

func UserFromContext(ctx context.Context) *User {
	u, _ := ctx.Value(contextKey{}).(*User)
	return u
}

func GenerateAPIKey() (fullKey, prefix string, err error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("auth.GenerateAPIKey: %w", err)
	}
	h := hex.EncodeToString(b)
	fullKey = "sk-" + h
	prefix = fullKey[:keyPrefixLen]
	return fullKey, prefix, nil
}

func HashAPIKey(key string) (string, error) {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:]), nil
}

func CheckAPIKey(storedHash, key string) bool {
	h := sha256.Sum256([]byte(key))
	expected, err := hex.DecodeString(storedHash)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(h[:], expected) == 1
}
