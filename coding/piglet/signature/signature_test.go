package signature

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/ownerfile"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return private
}

func publicOf(key ed25519.PrivateKey) ed25519.PublicKey {
	return key.Public().(ed25519.PublicKey)
}

func testManifest() Manifest {
	return Manifest{
		Piglet: "small", Target: "linux/amd64", PigVersion: "0.0.0-test",
		PigletDigest: "sha256:" + strings.Repeat("a", 64), SourceDigest: "sha256:" + strings.Repeat("b", 64),
		ResolutionDigest: "sha256:" + strings.Repeat("c", 64), ComponentPlanDigest: "sha256:" + strings.Repeat("d", 64),
		Components: []Component{{Kind: "extension", Name: "hello", Realization: "fused", Materialization: "binary", Digest: "sha256:" + strings.Repeat("e", 64)}},
		Embedded:   []EmbeddedFile{},
	}
}

// signedFixture writes a fake executable and signs it with key.
func signedFixture(t *testing.T, key ed25519.PrivateKey) (string, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pig-small")
	executable := make([]byte, 2048)
	if _, err := rand.Read(executable); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, executable, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Sign(path, testManifest(), key); err != nil {
		t.Fatal(err)
	}
	signed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, signed
}

func TestSignedBinaryVerifiesAndNamesItsSigner(t *testing.T) {
	key := newKey(t)
	path, _ := signedFixture(t, key)
	status, err := Check(path, Policy{Embedded: []ed25519.PublicKey{publicOf(key)}, RequireKnownSigner: true})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Signed || !status.Embedded || status.Trusted || status.KeyID != KeyID(publicOf(key)) {
		t.Fatalf("status = %+v", status)
	}
	if status.Manifest.Piglet != "small" || status.Manifest.Executable.Size != 2048 {
		t.Fatalf("manifest = %+v", status.Manifest)
	}
	if got := status.Describe(); !strings.Contains(got, "signed by "+status.KeyID) || !strings.Contains(got, "not in your Piglet trust store") {
		t.Fatalf("Describe() = %q", got)
	}
	if _, err := Sign(path, testManifest(), key); err == nil || !strings.Contains(err.Error(), "already carries") {
		t.Fatalf("second Sign() error = %v", err)
	}
}

// Every single-byte change anywhere in a signed file, including the
// signature block and its footer, must make the author-keyed check refuse.
func TestAnyChangedByteRefuses(t *testing.T) {
	key := newKey(t)
	path, signed := signedFixture(t, key)
	policy := Policy{Embedded: []ed25519.PublicKey{publicOf(key)}, RequireKnownSigner: true}
	for offset := range signed {
		tampered := append([]byte(nil), signed...)
		tampered[offset] ^= 0x01
		if err := os.WriteFile(path, tampered, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Check(path, policy); err == nil {
			t.Fatalf("byte %d of %d changed and the check still passed", offset, len(signed))
		}
	}
}

func TestResigningWithAnotherKeyRefuses(t *testing.T) {
	author, attacker := newKey(t), newKey(t)
	path, signed := signedFixture(t, author)
	status, err := Check(path, Policy{})
	if err != nil {
		t.Fatal(err)
	}
	stripped := signed[:status.Manifest.Executable.Size]
	if err := os.WriteFile(path, stripped, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Sign(path, testManifest(), attacker); err != nil {
		t.Fatal(err)
	}
	_, err = Check(path, Policy{Embedded: []ed25519.PublicKey{publicOf(author)}, RequireKnownSigner: true})
	if err == nil || !strings.Contains(err.Error(), "neither its author's embedded key nor in your Piglet trust store") {
		t.Fatalf("wrong-key error = %v", err)
	}
	if err := os.WriteFile(path, stripped, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = Check(path, Policy{Embedded: []ed25519.PublicKey{publicOf(author)}, RequireKnownSigner: true})
	if err == nil || !strings.Contains(err.Error(), "signature is missing") {
		t.Fatalf("stripped-signature error = %v", err)
	}
}

func TestUnsignedReportsUnsignedUnlessPolicyRequiresSignature(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pig-small")
	if err := os.WriteFile(path, []byte("unsigned executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	status, err := Check(path, Policy{RequireKnownSigner: true})
	if err != nil || status.Signed {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
	if !strings.HasPrefix(status.Describe(), "unsigned") {
		t.Fatalf("Describe() = %q", status.Describe())
	}
	_, err = Check(path, Policy{Trust: Trust{Dir: "/trust", RequireSignature: true}})
	if err == nil || !strings.Contains(err.Error(), "requires a signature") {
		t.Fatalf("require-signature error = %v", err)
	}
}

func TestTrustPolicyDecidesSigner(t *testing.T) {
	key := newKey(t)
	path, _ := signedFixture(t, key)
	id := KeyID(publicOf(key))
	trusted := Trust{Dir: "/trust", Keys: map[string]ed25519.PublicKey{id: publicOf(key)}}

	status, err := Check(path, Policy{Trust: trusted, RequireKnownSigner: true})
	if err != nil || !status.Trusted || !strings.Contains(status.Describe(), "a key in your Piglet trust store") {
		t.Fatalf("trusted status = %+v, err = %v", status, err)
	}
	if _, err := Check(path, Policy{RequireKnownSigner: true}); err == nil {
		t.Fatal("unknown signer passed a known-signer check")
	}
	requireTrusted := Policy{Trust: Trust{Dir: "/trust", RequireSignature: true}, Embedded: []ed25519.PublicKey{publicOf(key)}}
	if _, err := Check(path, requireTrusted); err == nil || !strings.Contains(err.Error(), "requires a trusted signature") {
		t.Fatalf("embedded-only signer under require-signature: err = %v", err)
	}
	revoked := Policy{Trust: Trust{Dir: "/trust", Revoked: map[string]bool{id: true}}, Embedded: []ed25519.PublicKey{publicOf(key)}}
	if _, err := Check(path, revoked); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("revoked signer: err = %v", err)
	}
}

func TestKeygenAndTrustStore(t *testing.T) {
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "author.key")
	id, err := GenerateKey(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if ownerOnly, err := ownerfile.OwnerOnly(privatePath, info); err != nil || !ownerOnly || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode = %v, owner-only = %t, err = %v", info.Mode(), ownerOnly, err)
	}
	if _, err := GenerateKey(privatePath); err == nil {
		t.Fatal("keygen replaced an existing key")
	}
	private, err := ReadPrivateKey(privatePath)
	if err != nil || KeyID(publicOf(private)) != id {
		t.Fatalf("ReadPrivateKey() id mismatch, err = %v", err)
	}
	if _, err := ReadPrivateKey(privatePath + ".pub"); err == nil {
		t.Fatal("a public key was accepted as a signing key")
	}

	store := filepath.Join(dir, "trust")
	publicPEM, err := os.ReadFile(privatePath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	if ids, err := TrustKeys(store, publicPEM); err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("TrustKeys() = %v, %v", ids, err)
	}
	if err := SetRequireSignature(store, true); err != nil {
		t.Fatal(err)
	}
	trust, err := LoadTrust(store)
	if err != nil || !trust.RequireSignature || trust.SortedKeyIDs()[0] != id {
		t.Fatalf("LoadTrust() = %+v, %v", trust, err)
	}
	if err := RevokeKey(store, id); err != nil {
		t.Fatal(err)
	}
	if trust, err = LoadTrust(store); err != nil || len(trust.Keys) != 0 || !trust.Revoked[id] {
		t.Fatalf("after revoke: %+v, %v", trust, err)
	}
	if _, err := TrustKeys(store, publicPEM); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("re-trusting a revoked key: err = %v", err)
	}
	if err := SetRequireSignature(store, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "revoked"), []byte("not-a-key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTrust(store); err == nil {
		t.Fatal("malformed revocation list was accepted")
	}
}

func TestKeygenDoesNotLeaveHalfAKeyPair(t *testing.T) {
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "author.key")
	if err := os.WriteFile(privatePath+".pub", []byte("reserved"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateKey(privatePath); err == nil {
		t.Fatal("GenerateKey() replaced an existing public-key path")
	}
	if _, err := os.Stat(privatePath); !os.IsNotExist(err) {
		t.Fatalf("failed keygen left a private key behind: %v", err)
	}
	if data, err := os.ReadFile(privatePath + ".pub"); err != nil || string(data) != "reserved" {
		t.Fatalf("existing public-key path changed: data=%q err=%v", data, err)
	}
}

type failingKeyFile struct {
	writeErr error
	closeErr error
	short    bool
}

func (f *failingKeyFile) Write(data []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	if f.short {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func (f *failingKeyFile) Close() error { return f.closeErr }

func TestKeygenRollsBackEveryPostCreateFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failOpen  int
		failWrite bool
		short     bool
	}{
		{name: "private write", failOpen: 1, failWrite: true},
		{name: "private short write", failOpen: 1, short: true},
		{name: "private close", failOpen: 1},
		{name: "public write", failOpen: 2, failWrite: true},
		{name: "public short write", failOpen: 2, short: true},
		{name: "public close", failOpen: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]bool{}
			openCount := 0
			openFile := func(path string, _ int, _ os.FileMode) (io.WriteCloser, error) {
				openCount++
				files[path] = true
				file := &failingKeyFile{}
				if openCount == tc.failOpen {
					switch {
					case tc.failWrite:
						file.writeErr = errors.New("injected write failure")
					case tc.short:
						file.short = true
					default:
						file.closeErr = errors.New("injected close failure")
					}
				}
				return file, nil
			}
			removeFile := func(path string) error {
				delete(files, path)
				return nil
			}
			if _, err := generateKey("author.key", openFile, removeFile); err == nil {
				t.Fatal("generateKey() succeeded after an injected failure")
			}
			if len(files) != 0 {
				t.Fatalf("failed keygen left created paths: %v", files)
			}
		})
	}
}

// firstWriteHook runs before its first Write.
type firstWriteHook struct {
	io.WriteCloser
	before func()
}

func (h *firstWriteHook) Write(p []byte) (int, error) {
	if h.before != nil {
		h.before()
		h.before = nil
	}
	return h.WriteCloser.Write(p)
}

// The private key file is owner-only before its first byte is written, even
// in a directory whose new files others can read. Restricting it after the
// write would expose the key to anyone who opened it in between.
func TestKeygenPrivateKeyIsOwnerOnlyBeforeItsFirstByte(t *testing.T) {
	dir := t.TempDir()
	testenv.InheritOthersRead(t, dir)
	privatePath := filepath.Join(dir, "author.key")
	checked := false
	open := func(path string, flag int, mode os.FileMode) (io.WriteCloser, error) {
		file, err := openKeyFile(path, flag, mode)
		if err != nil || path != privatePath {
			return file, err
		}
		return &firstWriteHook{WriteCloser: file, before: func() {
			checked = true
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if ownerOnly, err := ownerfile.OwnerOnly(path, info); err != nil || !ownerOnly {
				t.Errorf("private key file owner-only = %t (err %v) before its first byte; others could open it", ownerOnly, err)
			}
		}}, nil
	}
	if _, err := generateKey(privatePath, open, os.Remove); err != nil {
		t.Fatal(err)
	}
	if !checked {
		t.Fatal("generateKey wrote no private key")
	}
}

func TestMissingTrustStoreIsEmpty(t *testing.T) {
	trust, err := LoadTrust(filepath.Join(t.TempDir(), "absent"))
	if err != nil || len(trust.Keys) != 0 || trust.RequireSignature {
		t.Fatalf("LoadTrust(absent) = %+v, %v", trust, err)
	}
}

// BenchmarkCheckSigned32MiB measures the startup cost a signed Piglet Binary
// pays: one SHA-256 pass over its executable bytes plus one ed25519 verify.
func BenchmarkCheckSigned32MiB(b *testing.B) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(b.TempDir(), "pig-large")
	if err := os.WriteFile(path, make([]byte, 32<<20), 0o755); err != nil {
		b.Fatal(err)
	}
	if _, err := Sign(path, testManifest(), key); err != nil {
		b.Fatal(err)
	}
	policy := Policy{Embedded: []ed25519.PublicKey{publicOf(key)}, RequireKnownSigner: true}
	b.SetBytes(32 << 20)
	for b.Loop() {
		if _, err := Check(path, policy); err != nil {
			b.Fatal(err)
		}
	}
}
