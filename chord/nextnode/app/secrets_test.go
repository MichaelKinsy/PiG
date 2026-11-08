package app

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Ports packages/chord/test/node/app/secrets.test.ts: the kit's secrets (slice 13 §2): random tokens, hashes compared in
// constant time, and secret files written with mode 0600 in one step (a create never overwrites, a replace swaps whole
// files, no temporary file left).

func TestSecretsTokensAndHashes(t *testing.T) {
	token := NewToken()
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(token) {
		t.Fatalf("token = %q", token)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{16}$`).MatchString(NewToken(12)) {
		t.Fatal("a 12-byte token is not 16 base64url characters")
	}
	seen := map[string]bool{}
	for range 100 {
		seen[NewToken()] = true
	}
	if len(seen) != 100 {
		t.Fatalf("distinct tokens = %d, want 100", len(seen))
	}
	hash := HashToken(token)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hash) {
		t.Fatalf("hash = %q", hash)
	}
	if !MatchesHash(token, hash) || MatchesHash(token+"x", hash) || MatchesHash(token, "short") {
		t.Fatal("MatchesHash does not match only the token's own hash")
	}
}

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestSecretFilesCreateNeverOverwritesReplaceSwapsWholeLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if _, found, err := ReadSecret(path); err != nil || found {
		t.Fatalf("ReadSecret(missing) = %v, %v", found, err)
	}
	if got, err := WriteSecret(path, "one", SecretCreate); err != nil || got != SecretWritten {
		t.Fatalf("create = %v, %v", got, err)
	}
	if mode := modeOf(t, path); mode != 0o600 {
		t.Fatalf("mode = %o", mode)
	}
	if got, err := WriteSecret(path, "two", SecretCreate); err != nil || got != SecretExists {
		t.Fatalf("second create = %v, %v", got, err)
	}
	if text, found, err := ReadSecret(path); err != nil || !found || text != "one" {
		t.Fatalf("ReadSecret = %q, %v, %v", text, found, err)
	}
	if got, err := WriteSecret(path, "three", SecretReplace); err != nil || got != SecretWritten {
		t.Fatalf("replace = %v, %v", got, err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "three\n" {
		t.Fatalf("file = %q", raw)
	}
	if mode := modeOf(t, path); mode != 0o600 {
		t.Fatalf("mode after replace = %o", mode)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "secret" {
		t.Fatalf("directory = %v", entries)
	}
}

func TestSecretFileKeepsItsFirstValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookie-secret")
	first, err := SecretFile(path)
	if err != nil || !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(first) {
		t.Fatalf("first = %q, %v", first, err)
	}
	again, err := SecretFile(path)
	if err != nil || again != first {
		t.Fatalf("again = %q, %v", again, err)
	}
	if mode := modeOf(t, path); mode != 0o600 {
		t.Fatalf("mode = %o", mode)
	}
}
