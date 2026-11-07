// Package onboarding is a new instance's first run: the claim code that makes
// the first owner, the getting-started checklist, and the sample data.
package onboarding

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ClaimCookie carries a verified claim code through sign-up, including an
// OAuth round trip.
const ClaimCookie = "skael_claim"

// SampleDomain marks the sample people.
const SampleDomain = "@sample.skael.invalid"

// humansSQL counts real people: no service account and no sample person.
const humansSQL = `SELECT COUNT(*) FROM auth_users WHERE kind = 'human' AND email_normalized NOT LIKE '%` + SampleDomain + `'`

// Claim guards a fresh instance: until a person exists, signing up needs the
// code the server prints to its log.
type Claim struct{ pool *pgxpool.Pool }

// NewClaim returns the guard.
func NewClaim(pool *pgxpool.Pool) *Claim { return &Claim{pool: pool} }

// Open reports whether the instance has no person yet.
func (c *Claim) Open(ctx context.Context) (bool, error) {
	var n int
	if err := c.pool.QueryRow(ctx, humansSQL).Scan(&n); err != nil {
		return false, fmt.Errorf("onboarding: count people: %w", err)
	}
	return n == 0, nil
}

// Ensure returns the claim code while the instance has no person, creating it
// once. It returns "" once someone has signed up.
func (c *Claim) Ensure(ctx context.Context) (string, error) {
	open, err := c.Open(ctx)
	if err != nil || !open {
		return "", err
	}
	code, err := newCode()
	if err != nil {
		return "", err
	}
	var stored string
	err = c.pool.QueryRow(ctx, `UPDATE onboarding SET claim_code = CASE WHEN claim_code = '' THEN $1 ELSE claim_code END
		WHERE id = 1 RETURNING claim_code`, code).Scan(&stored)
	if err != nil {
		return "", fmt.Errorf("onboarding: claim code: %w", err)
	}
	return stored, nil
}

// Verify reports whether code is the claim code.
func (c *Claim) Verify(ctx context.Context, code string) (bool, error) {
	var stored string
	if err := c.pool.QueryRow(ctx, `SELECT claim_code FROM onboarding WHERE id = 1`).Scan(&stored); err != nil {
		return false, fmt.Errorf("onboarding: claim code: %w", err)
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	return stored != "" && subtle.ConstantTimeCompare([]byte(code), []byte(stored)) == 1, nil
}

// Claimed reports whether r carries the verified claim code.
func (c *Claim) Claimed(r *http.Request) bool {
	ck, err := r.Cookie(ClaimCookie)
	if err != nil {
		return false
	}
	ok, err := c.Verify(r.Context(), ck.Value)
	return err == nil && ok
}

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// newCode returns a code like 7KQM-X2PD-RW4N: 60 bits, no look-alike letters.
func newCode() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, v := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(codeAlphabet[int(v)%len(codeAlphabet)])
	}
	return sb.String(), nil
}
