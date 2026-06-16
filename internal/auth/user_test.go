package auth

import "testing"

func TestCheckAPIKey(t *testing.T) {
	key := "sk-test1234567890ab"
	hash, _ := HashAPIKey(key)
	if !CheckAPIKey(hash, key) {
		t.Fatal("expected true for correct key")
	}
	if CheckAPIKey(hash, "sk-wrong1234567890") {
		t.Fatal("expected false for wrong key")
	}
}
