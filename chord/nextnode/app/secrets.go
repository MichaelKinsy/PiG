package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Ports packages/chord/src/node/app/secrets.ts
//
// Secrets of the app kit (slice 13 §2): tokens from `crypto.randomBytes`, kept only as their SHA-256 hashes and compared in
// constant time, and secret files (the cookie key, the owner token file) created with mode 0600 in one step, so no other
// OS user and no half-written file ever reads them.
//
// Go forced differences: a function that rejects returns `(value, error)`; `readSecret`'s `undefined` is `found` false.

// NewToken is a fresh random token of bytes bytes (32 without an argument), base64url.
func NewToken(bytes ...int) string {
	size := 32
	if len(bytes) > 0 {
		size = bytes[0]
	}
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		panic("chord: no randomness: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// HashToken is the hash a token is kept as (SHA-256, hex): what leaks from a store is no credential.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// MatchesHash reports whether token hashes to hash, compared in constant time.
func MatchesHash(token, hash string) bool {
	given, _ := hex.DecodeString(HashToken(token))
	kept, err := hex.DecodeString(hash)
	if err != nil {
		// Node's Buffer.from(hash, "hex") keeps the bytes before the first invalid pair; no hash of a token has one.
		kept = kept[:0]
	}
	return len(given) == len(kept) && subtle.ConstantTimeCompare(given, kept) == 1
}

// SecretFile is the secret in the file at path, created (mode 0600, in one step) with a fresh token when it does not exist
// yet.
func SecretFile(path string) (string, error) {
	existing, found, err := ReadSecret(path)
	if err != nil {
		return "", err
	}
	if found {
		return existing, nil
	}
	secret := NewToken()
	written, err := WriteSecret(path, secret, SecretCreate)
	if err != nil {
		return "", err
	}
	if written == SecretExists {
		if raced, found, err := ReadSecret(path); err != nil {
			return "", err
		} else if found {
			return raced, nil
		}
	}
	return secret, nil
}

// ReadSecret is the text of the secret file at path; found is false when it does not exist.
func ReadSecret(path string) (text string, found bool, err error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(raw)), true, nil
}

// SecretMode is how WriteSecret puts a file in place.
type SecretMode string

// The modes of WriteSecret.
const (
	// SecretCreate links the file into place: refused when path exists (SecretExists is returned).
	SecretCreate SecretMode = "create"
	// SecretReplace renames the file over path.
	SecretReplace SecretMode = "replace"
)

// SecretOutcome is what WriteSecret did.
type SecretOutcome string

// The outcomes of WriteSecret.
const (
	SecretWritten SecretOutcome = "written"
	SecretExists  SecretOutcome = "exists"
)

// WriteSecret writes text to path with mode 0600 in one step: a temporary file of the same directory (made exclusively,
// 0600) linked into place (SecretCreate: refused when path exists, SecretExists returned) or renamed over it
// (SecretReplace).
func WriteSecret(path, text string, mode SecretMode) (SecretOutcome, error) {
	temporary := filepath.Join(filepath.Dir(path), "."+NewToken(9)+".tmp")
	// 0600 exactly, whatever the umask: the file is made with it and the process umask only removes bits.
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(temporary) }()
	if _, err := file.WriteString(text + "\n"); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if mode == SecretReplace {
		if err := os.Rename(temporary, path); err != nil {
			return "", err
		}
		return SecretWritten, nil
	}
	if err := os.Link(temporary, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return SecretExists, nil
		}
		return "", err
	}
	return SecretWritten, nil
}
