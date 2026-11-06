package platform_test

import (
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/platform"
)

const (
	keyA = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	keyB = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA="
)

// A rotated key list must still open what the old active key sealed, and a
// value moved to another column must not open.
func TestKeyring_RotatesAndBindsTheValueToItsPlace(t *testing.T) {
	old, err := platform.ParseKeyring("k1:" + keyA)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := old.Seal([]byte("pem"), "github_app.private_key")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "pem") || !strings.HasPrefix(sealed, "k1:") {
		t.Fatalf("sealed value %q leaks or lacks its key id", sealed)
	}

	rotated, err := platform.ParseKeyring("k2:" + keyB + ",k1:" + keyA)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := rotated.Open(sealed, "github_app.private_key")
	if err != nil || string(plain) != "pem" {
		t.Fatalf("Open after rotation = %q, %v", plain, err)
	}
	resealed, err := rotated.Seal(plain, "github_app.private_key")
	if err != nil || !strings.HasPrefix(resealed, "k2:") {
		t.Fatalf("Seal after rotation = %q, %v, want the new active key", resealed, err)
	}
	if _, err := rotated.Open(sealed, "github_app.webhook_secret"); err == nil {
		t.Error("a value opened under another column's associated data")
	}
}

func TestParseKeyring_RefusesAShortKeyAndADuplicateID(t *testing.T) {
	for _, raw := range []string{"k1:c2hvcnQ=", "k1:" + keyA + ",k1:" + keyB, "nocolon"} {
		if _, err := platform.ParseKeyring(raw); err == nil {
			t.Errorf("ParseKeyring(%q) accepted it", raw)
		}
	}
	empty, err := platform.ParseKeyring("")
	if err != nil || empty.Enabled() {
		t.Errorf("an empty value = %v, %v, want a keyring with no keys", empty.Enabled(), err)
	}
}
