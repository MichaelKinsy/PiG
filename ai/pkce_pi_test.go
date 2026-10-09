package ai

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"testing"
)

// pi: packages/ai/src/auth/oauth/pkce.ts

// generatePKCE (pkce.ts:17-31): the verifier is 32 random bytes as unpadded base64url, the challenge is the base64url SHA-256 of the
// verifier string, and every call draws fresh bytes.
func TestGeneratePKCEMatchesPi(t *testing.T) {
	url := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	a, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"verifier": a.Verifier, "challenge": a.Challenge} {
		if !url.MatchString(value) {
			t.Fatalf("%s %q is not unpadded base64url", name, value)
		}
	}
	raw, err := base64.RawURLEncoding.DecodeString(a.Verifier)
	if err != nil || len(raw) != 32 {
		t.Fatalf("verifier decodes to %d bytes (err %v), want 32", len(raw), err)
	}
	sum := sha256.Sum256([]byte(a.Verifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); a.Challenge != want {
		t.Fatalf("challenge = %q, want base64url(sha256(verifier)) = %q", a.Challenge, want)
	}
	b, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if a.Verifier == b.Verifier {
		t.Fatal("two calls returned the same verifier")
	}
}
