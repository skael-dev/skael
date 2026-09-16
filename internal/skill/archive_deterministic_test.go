package skill_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/skill"
)

// The checksum is the identity of the content: it decides whether a republish
// is a no-op, names the stored archive, and is what sync verifies. A header
// carrying the packing time or the packing user made two packs of the same
// bytes different, which surfaced as a publish test failing only when CI was
// slow enough to straddle a second.
func TestPack_SameContentPacksToTheSameBytes(t *testing.T) {
	pack := func() string {
		dir := t.TempDir()
		body := []byte("---\nname: demo\ndescription: d\n---\n# demo\nbody\n")
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), body, 0o644); err != nil {
			t.Fatal(err)
		}
		archive, checksum, _, err := skill.Pack(dir)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(archive)
		if got := hex.EncodeToString(sum[:]); got != checksum {
			t.Fatalf("Pack's checksum %s is not the archive's own hash %s", checksum, got)
		}
		return checksum
	}

	first := pack()
	// Long enough to cross the one-second resolution a tar header records.
	time.Sleep(1100 * time.Millisecond)
	if second := pack(); first != second {
		t.Errorf("same content packed to different checksums:\n  %s\n  %s", first, second)
	}
}

// A skill that ships a script needs the bit; nothing else about the mode
// survives, because Unpack masks permissions anyway.
func TestPack_KeepsOnlyTheExecutableBit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: demo\ndescription: d\n---\n# demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\necho hi\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	archive, _, _, err := skill.Pack(dir)
	if err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	if err := skill.Unpack(bytes.NewReader(archive), out); err != nil {
		t.Fatal(err)
	}
	script, err := os.Stat(filepath.Join(out, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if script.Mode().Perm()&0o111 == 0 {
		t.Errorf("run.sh unpacked as %v, want the executable bit kept", script.Mode().Perm())
	}
}
