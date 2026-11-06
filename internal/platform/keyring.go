package platform

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// ErrNoMasterKey means no master key can seal or open a value.
var ErrNoMasterKey = errors.New("no master key: set SKAEL_MASTER_KEYS")

// Keyring holds the master keys from SKAEL_MASTER_KEYS. The first key seals;
// every key opens, so a rotated-out key keeps old values readable until they
// are sealed again.
type Keyring struct {
	keys []masterKey
}

type masterKey struct {
	id  string
	key []byte
}

// ParseKeyring parses "id:base64key" pairs separated by commas. Each key must
// decode to 32 bytes. An empty value is a keyring with no keys.
func ParseKeyring(raw string) (*Keyring, error) {
	k := &Keyring{}
	if strings.TrimSpace(raw) == "" {
		return k, nil
	}
	seen := map[string]bool{}
	for _, pair := range strings.Split(raw, ",") {
		id, b64, ok := strings.Cut(strings.TrimSpace(pair), ":")
		if !ok || id == "" {
			return nil, fmt.Errorf("SKAEL_MASTER_KEYS: %q is not id:base64key", pair)
		}
		if seen[id] {
			return nil, fmt.Errorf("SKAEL_MASTER_KEYS: key id %q appears twice", id)
		}
		key, err := base64.StdEncoding.DecodeString(b64)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("SKAEL_MASTER_KEYS: key %q must be 32 bytes in base64; run openssl rand -base64 32", id)
		}
		seen[id] = true
		k.keys = append(k.keys, masterKey{id: id, key: key})
	}
	return k, nil
}

// Enabled reports whether a key exists to seal with.
func (k *Keyring) Enabled() bool { return k != nil && len(k.keys) > 0 }

// Seal encrypts plain with the active key under AES-256-GCM. aad binds the
// value to where it is stored, so a sealed value copied to another column
// does not open. The result is "keyid:" followed by base64 of nonce||ciphertext.
func (k *Keyring) Seal(plain []byte, aad string) (string, error) {
	if !k.Enabled() {
		return "", ErrNoMasterKey
	}
	active := k.keys[0]
	g, err := gcm(active.key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("keyring: nonce: %w", err)
	}
	sealed := g.Seal(nonce, nonce, plain, []byte(aad))
	return active.id + ":" + base64.StdEncoding.EncodeToString(sealed), nil
}

// Open decrypts a value Seal produced, with the key its id names.
func (k *Keyring) Open(value, aad string) ([]byte, error) {
	id, b64, ok := strings.Cut(value, ":")
	if !ok {
		return nil, errors.New("keyring: sealed value has no key id")
	}
	var key []byte
	if k != nil {
		for _, m := range k.keys {
			if m.id == id {
				key = m.key
			}
		}
	}
	if key == nil {
		return nil, fmt.Errorf("%w with id %q", ErrNoMasterKey, id)
	}
	sealed, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("keyring: sealed value: %w", err)
	}
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < g.NonceSize() {
		return nil, errors.New("keyring: sealed value too short")
	}
	return g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], []byte(aad))
}

func gcm(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}
