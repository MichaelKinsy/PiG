package binarypiglet

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

// signedSelf bakes a valid closure, makes the running "executable" a file
// signed with a manifest built by edit, and embeds the signer key.
func signedSelf(t *testing.T, edit func(*signature.Manifest)) {
	t.Helper()
	yaml := []byte("name: t\n")
	record := resolutionFor(t, yaml)
	swapBaked(t, yaml, marshal(t, record))
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pig-t")
	if err := os.WriteFile(path, []byte("executable bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := signature.Manifest{
		Piglet: "t", ReleaseVersion: "1.0.0", Target: runtime.GOOS + "/" + runtime.GOARCH, PigVersion: coding.PigVersion,
		PigletDigest: record.Resolution.EffectiveDigest, SourceDigest: record.Resolution.SourceDigest,
		ResolutionDigest: record.Digest, ComponentPlanDigest: record.Resolution.ComponentPlan.Digest,
	}
	edit(&manifest)
	if _, err := signature.Sign(path, manifest, key); err != nil {
		t.Fatal(err)
	}
	signer, err := signature.MarshalPublicKey(key.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	oldExecutable, oldSigners := executable, signersPEM
	executable = func() (string, error) { return path, nil }
	signersPEM = signer
	t.Cleanup(func() { executable, signersPEM = oldExecutable, oldSigners })
}

func TestVerify_SignedBinaryBoundToItsBuildPassesAndIsRemembered(t *testing.T) {
	signedSelf(t, func(*signature.Manifest) {})
	for range 2 {
		if cacheErr, err := Verify(); err != nil || cacheErr != nil {
			t.Fatalf("Verify() = %v, %v", cacheErr, err)
		}
	}
	path, _ := executable()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(verifyCachePath())
	if err != nil {
		t.Fatalf("a passing check was not remembered: %v", err)
	}
	quoted, err := json.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), string(quoted)) {
		t.Fatalf("verification cache %s names no entry for %s", data, resolved)
	}
}

// A validly signed manifest for a different build must not vouch for this one,
// and its passing signature check must not be remembered.
func TestVerify_SignedManifestForAnotherBuildFails(t *testing.T) {
	for name, edit := range map[string]func(*signature.Manifest){
		"record":  func(m *signature.Manifest) { m.ResolutionDigest = "sha256:" + strings.Repeat("f", 64) },
		"piglet":  func(m *signature.Manifest) { m.PigletDigest = "sha256:" + strings.Repeat("f", 64) },
		"target":  func(m *signature.Manifest) { m.Target = "plan9/mips" },
		"version": func(m *signature.Manifest) { m.PigVersion = "0.0.0-other" },
	} {
		t.Run(name, func(t *testing.T) {
			signedSelf(t, edit)
			for range 2 {
				if _, err := Verify(); err == nil || !strings.Contains(err.Error(), "signed Piglet manifest names") {
					t.Fatalf("Verify() = %v, want a manifest binding error", err)
				}
			}
			if _, err := os.Stat(verifyCachePath()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a failed verification was remembered: %v", err)
			}
		})
	}
}

// A cache that cannot be written never stops a verified Binary from starting.
func TestVerify_CacheWriteFailureIsReportedNotFatal(t *testing.T) {
	signedSelf(t, func(*signature.Manifest) {})
	blocker := filepath.Dir(verifyCachePath())
	if err := os.MkdirAll(filepath.Dir(blocker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cacheErr, err := Verify()
	if err != nil {
		t.Fatalf("Verify() = %v, want a passing check", err)
	}
	if cacheErr == nil || !strings.Contains(cacheErr.Error(), "Piglet verification cache") {
		t.Fatalf("Verify() cache error = %v, want a verification cache write failure", cacheErr)
	}
}

func TestVerify_StrippedSignatureWithEmbeddedSignerFails(t *testing.T) {
	signedSelf(t, func(*signature.Manifest) {})
	path, _ := executable()
	if err := os.WriteFile(path, []byte("executable bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(); err == nil || !strings.Contains(err.Error(), "signature is missing") {
		t.Fatalf("Verify() = %v, want a missing-signature error", err)
	}
}

func TestVerify_UnsignedBinaryFollowsRequireSignaturePolicy(t *testing.T) {
	yaml := []byte("name: t\n")
	swapBaked(t, yaml, marshal(t, resolutionFor(t, yaml)))
	if _, err := Verify(); err != nil {
		t.Fatalf("unsigned Verify() = %v, want nil", err)
	}
	if err := signature.SetRequireSignature(signature.TrustDir(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(); err == nil || !strings.Contains(err.Error(), "requires a signature") {
		t.Fatalf("Verify() under require-signature = %v", err)
	}
}

// Turning trust require on after a remembered check still refuses a Binary
// signed by a key outside the trust store.
func TestVerify_RequireSignatureAppliesToARememberedBinary(t *testing.T) {
	signedSelf(t, func(*signature.Manifest) {})
	if _, err := Verify(); err != nil {
		t.Fatal(err)
	}
	if err := signature.SetRequireSignature(signature.TrustDir(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(); err == nil || !strings.Contains(err.Error(), "requires a trusted signature") {
		t.Fatalf("Verify() under require-signature = %v", err)
	}
}
