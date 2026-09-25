package ai

// Mirrors upstream .upstream/current/packages/ai/src/utils/oauth/pkce.ts.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// PKCE holds a code verifier and its corresponding S256 challenge.
type PKCE struct {
	Verifier  string
	Challenge string
}

// base64URLEncode encodes bytes as unpadded base64url.
func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// GeneratePKCE creates a random PKCE verifier and SHA-256 challenge.
func GeneratePKCE() (PKCE, error) {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return PKCE{}, err
	}
	verifier := base64URLEncode(verifierBytes)

	hash := sha256.Sum256([]byte(verifier))
	challenge := base64URLEncode(hash[:])

	return PKCE{Verifier: verifier, Challenge: challenge}, nil
}
