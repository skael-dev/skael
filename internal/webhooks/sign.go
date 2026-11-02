package webhooks

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// The headers each delivery carries.
const (
	HeaderEventID   = "X-Skael-Event-Id"
	HeaderEventType = "X-Skael-Event-Type"
	HeaderTimestamp = "X-Skael-Timestamp"
	HeaderSignature = "X-Skael-Signature"
)

// Sign returns the hex HMAC-SHA256 of "<timestamp>.<body>".
func Sign(secret string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", timestamp)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// SignatureHeader signs with each live secret, so a consumer holding the old
// secret still verifies during a rotation window.
func SignatureHeader(timestamp int64, body []byte, secrets ...string) string {
	header := ""
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if header != "" {
			header += ","
		}
		header += "v1=" + Sign(secret, timestamp, body)
	}
	return header
}

func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("webhooks: secret: %w", err)
	}
	return "whsec_" + hex.EncodeToString(b), nil
}
