package release

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

const testTarget = "testos/testarch"

func TestPullInstallsSignedReleaseAndListsBinaryFacet(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	key := newKey(t)
	binary := signedBinary(t, key, "porter", "1.2.3", testTarget, "pig-test")
	server, indexURL := releaseServer(t, key, releaseSpec{
		piglet: "porter", version: "1.2.3", target: testTarget, pigVersion: "pig-test", binary: binary,
	})
	defer server.Close()

	result, err := Pull(context.Background(), indexURL, Options{
		Target: testTarget,
		Now:    func() time.Time { return time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Piglet != "porter" || result.Version != "1.2.3" || result.Target != testTarget || result.SignerKeyID != signature.KeyID(key.Public().(ed25519.PublicKey)) {
		t.Fatalf("result = %#v", result)
	}
	if data, err := os.ReadFile(result.Artifact); err != nil || !reflect.DeepEqual(data, binary) {
		t.Fatalf("artifact data mismatch: err=%v", err)
	}
	installed, errs := ListInstalled()
	if len(errs) != 0 || len(installed) != 1 {
		t.Fatalf("installed=%#v errs=%v", installed, errs)
	}
	if installed[0].ReceiptPath != result.Receipt || installed[0].ArtifactPath != result.Artifact || installed[0].Manifest.Piglet != "porter" {
		t.Fatalf("installed = %#v", installed[0])
	}
}

func TestPulledReceiptBindsArtifactToSignedReleaseIndex(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	key := newKey(t)
	binary := signedBinary(t, key, "porter", "1.2.3", testTarget, "pig-test")
	server, indexURL := releaseServer(t, key, releaseSpec{
		piglet: "porter", version: "1.2.3", target: testTarget, pigVersion: "pig-test", binary: binary,
	})
	defer server.Close()
	result, err := Pull(context.Background(), indexURL, Options{Target: testTarget})
	if err != nil {
		t.Fatal(err)
	}

	replacementPath := filepath.Join(t.TempDir(), "replacement")
	if err := os.WriteFile(replacementPath, []byte("different executable\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	replacementManifest, err := signature.Sign(replacementPath, signature.Manifest{
		Piglet: "porter", ReleaseVersion: "1.2.3", Target: testTarget, PigVersion: "pig-test",
		PigletDigest: digest, SourceDigest: digest, ResolutionDigest: digest, ComponentPlanDigest: digest,
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := os.ReadFile(replacementPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(result.Artifact, replacement, 0o755); err != nil {
		t.Fatal(err)
	}
	receiptData, err := os.ReadFile(result.Receipt)
	if err != nil {
		t.Fatal(err)
	}
	var receipt Receipt
	if err := json.Unmarshal(receiptData, &receipt); err != nil {
		t.Fatal(err)
	}
	replacementSum := sha256.Sum256(replacement)
	receipt.Manifest = replacementManifest
	receipt.Artifact.Digest = "sha256:" + hex.EncodeToString(replacementSum[:])
	receipt.Artifact.Size = int64(len(replacement))
	receiptData, err = json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(result.Receipt, append(receiptData, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	installed, errs := ListInstalled()
	if len(installed) != 0 || !containsError(errs, "does not match its signed release index") {
		t.Fatalf("installed=%#v errs=%v", installed, errs)
	}
}

func TestPullNegativeCasesLeaveNoPartialFiles(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T, ed25519.PrivateKey)
		edit  func(*testing.T, *releaseSpec)
		want  string
	}{
		{
			name: "sha mismatch",
			edit: func(_ *testing.T, spec *releaseSpec) { spec.sha256 = strings.Repeat("0", 64) },
			want: "SHA256",
		},
		{
			name: "size mismatch",
			edit: func(_ *testing.T, spec *releaseSpec) { spec.size = int64(len(spec.binary) + 1) },
			want: "size is",
		},
		{
			name: "size above bounded download limit",
			edit: func(_ *testing.T, spec *releaseSpec) { spec.size = maxBinaryBytes + 1 },
			want: "above the",
		},
		{
			name: "trailer tampered",
			edit: func(_ *testing.T, spec *releaseSpec) {
				spec.binary = append([]byte(nil), spec.binary...)
				spec.binary[0] ^= 0xff
			},
			want: "executable bytes changed",
		},
		{
			name: "index signature bad",
			edit: func(_ *testing.T, spec *releaseSpec) { spec.badIndexSignature = true },
			want: "does not verify",
		},
		{
			name: "revoked key",
			setup: func(t *testing.T, key ed25519.PrivateKey) {
				t.Helper()
				if err := signature.RevokeKey(signature.TrustDir(), signature.KeyID(key.Public().(ed25519.PublicKey))); err != nil {
					t.Fatal(err)
				}
			},
			want: "revoked",
		},
		{
			name: "require signature with untrusted key",
			setup: func(t *testing.T, _ ed25519.PrivateKey) {
				t.Helper()
				if err := signature.SetRequireSignature(signature.TrustDir(), true); err != nil {
					t.Fatal(err)
				}
			},
			want: "requires a trusted signature",
		},
		{
			name: "wrong target in binary manifest",
			edit: func(t *testing.T, spec *releaseSpec) {
				t.Helper()
				spec.binary = signedBinary(t, spec.key, spec.piglet, spec.version, "wrong/target", spec.pigVersion)
			},
			want: "manifest names target",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PIG_HOME", t.TempDir())
			t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
			key := newKey(t)
			if tc.setup != nil {
				tc.setup(t, key)
			}
			before := snapshotTree(t, os.Getenv("PIG_HOME"))
			spec := releaseSpec{
				key: key, piglet: "porter", version: "1.2.3", target: testTarget,
				pigVersion: "pig-test", binary: signedBinary(t, key, "porter", "1.2.3", testTarget, "pig-test"),
			}
			if tc.edit != nil {
				tc.edit(t, &spec)
			}
			server, indexURL := releaseServer(t, key, spec)
			defer server.Close()
			_, err := Pull(context.Background(), indexURL, Options{Target: testTarget})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
			after := snapshotTree(t, os.Getenv("PIG_HOME"))
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("failed pull changed managed state:\nbefore=%#v\nafter=%#v", before, after)
			}
		})
	}
}

func TestPullPinsFirstSignerAndRequiresExplicitRotation(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	firstKey := newKey(t)
	firstBinary := signedBinary(t, firstKey, "porter", "1.0.0", testTarget, "pig-test")
	firstServer, firstURL := releaseServer(t, firstKey, releaseSpec{
		piglet: "porter", version: "1.0.0", target: testTarget, pigVersion: "pig-test", binary: firstBinary,
	})
	if _, err := Pull(context.Background(), firstURL, Options{Target: testTarget}); err != nil {
		t.Fatal(err)
	}
	firstServer.Close()

	secondKey := newKey(t)
	secondID := signature.KeyID(secondKey.Public().(ed25519.PublicKey))
	secondBinary := signedBinary(t, secondKey, "porter", "2.0.0", testTarget, "pig-test")
	secondServer, secondURL := releaseServer(t, secondKey, releaseSpec{
		piglet: "porter", version: "2.0.0", target: testTarget, pigVersion: "pig-test", binary: secondBinary,
	})
	defer secondServer.Close()
	before := snapshotTree(t, os.Getenv("PIG_HOME"))
	if _, err := Pull(context.Background(), secondURL, Options{Target: testTarget}); err == nil || !strings.Contains(err.Error(), "pinned to signer") {
		t.Fatalf("signer change error = %v", err)
	}
	if after := snapshotTree(t, os.Getenv("PIG_HOME")); !reflect.DeepEqual(after, before) {
		t.Fatalf("refused signer rotation changed state:\nbefore=%#v\nafter=%#v", before, after)
	}
	if _, err := Pull(context.Background(), secondURL, Options{Target: testTarget, AcceptSigner: secondID}); err != nil {
		t.Fatalf("explicit signer rotation: %v", err)
	}
	current, err := readCurrent("porter")
	if err != nil || current.SignerKeyID != secondID {
		t.Fatalf("current=%#v err=%v", current, err)
	}
}

func TestResolveIndexURLBindsGitHubRefVersion(t *testing.T) {
	url, version, err := resolveIndexURL("github:acme/porter@v1.2.3", "")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://github.com/acme/porter/releases/download/v1.2.3/piglet-release.json" || version != "1.2.3" {
		t.Fatalf("resolveIndexURL() = %q, %q", url, version)
	}
	if _, _, err := resolveIndexURL("github:acme/porter@1.2.3", "2.0.0"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched --version error = %v", err)
	}
	if _, _, err := resolveIndexURL("github:acme/porter@../../other", ""); err == nil {
		t.Fatal("non-SemVer GitHub release was accepted")
	}
	if _, _, err := resolveIndexURL("github:acme?redirect/porter@1.2.3", ""); err == nil {
		t.Fatal("URL metacharacter in GitHub owner was accepted")
	}
	if _, _, err := resolveIndexURL("https://api.pi.dev/piglet-release.json", ""); err == nil || !strings.Contains(err.Error(), "Earendil-operated") {
		t.Fatalf("pi.dev release URL error = %v", err)
	}
}

func TestPullRejectsUnavailableTargetWithoutFiles(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	key := newKey(t)
	binary := signedBinary(t, key, "porter", "1.2.3", testTarget, "pig-test")
	server, indexURL := releaseServer(t, key, releaseSpec{
		piglet: "porter", version: "1.2.3", target: testTarget, pigVersion: "pig-test", binary: binary,
	})
	defer server.Close()
	before := snapshotTree(t, os.Getenv("PIG_HOME"))
	if _, err := Pull(context.Background(), indexURL, Options{Target: "other/target"}); err == nil || !strings.Contains(err.Error(), "no binary for target") {
		t.Fatalf("error = %v", err)
	}
	if after := snapshotTree(t, os.Getenv("PIG_HOME")); !reflect.DeepEqual(after, before) {
		t.Fatalf("wrong target changed state: before=%#v after=%#v", before, after)
	}
}

type releaseSpec struct {
	key               ed25519.PrivateKey
	piglet            string
	version           string
	target            string
	pigVersion        string
	binary            []byte
	sha256            string
	size              int64
	badIndexSignature bool
}

func releaseServer(t *testing.T, key ed25519.PrivateKey, spec releaseSpec) (*httptest.Server, string) {
	t.Helper()
	if spec.key == nil {
		spec.key = key
	}
	var indexData []byte
	ready := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		<-ready
		switch request.URL.Path {
		case "/piglet-release.json":
			_, _ = w.Write(indexData)
		case "/binary":
			_, _ = w.Write(spec.binary)
		default:
			http.NotFound(w, request)
		}
	}))
	digest := sha256.Sum256(spec.binary)
	sha := hex.EncodeToString(digest[:])
	if spec.sha256 != "" {
		sha = spec.sha256
	}
	size := int64(len(spec.binary))
	if spec.size != 0 {
		size = spec.size
	}
	var err error
	indexData, err = Sign(Index{
		Piglet: spec.piglet, Version: spec.version, PigVersion: spec.pigVersion, SourceRef: "npm:@example/porter@" + spec.version,
		Binaries: map[string]Binary{spec.target: {URL: server.URL + "/binary", SHA256: sha, Size: size}},
	}, key)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	if spec.badIndexSignature {
		var envelope signature.Envelope
		if err := json.Unmarshal(indexData, &envelope); err != nil {
			t.Fatal(err)
		}
		sig, err := base64.StdEncoding.DecodeString(envelope.Signatures[0].Sig)
		if err != nil {
			t.Fatal(err)
		}
		sig[0] ^= 0xff
		envelope.Signatures[0].Sig = base64.StdEncoding.EncodeToString(sig)
		indexData, err = json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
	}
	close(ready)
	return server, server.URL + "/piglet-release.json"
}

func signedBinary(t *testing.T, key ed25519.PrivateKey, piglet, version, target, pigVersion string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "piglet")
	if err := os.WriteFile(path, []byte("test executable\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if _, err := signature.Sign(path, signature.Manifest{
		Piglet: piglet, ReleaseVersion: version, Target: target, PigVersion: pigVersion,
		PigletDigest: digest, SourceDigest: digest, ResolutionDigest: digest, ComponentPlanDigest: digest,
	}, key); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func containsError(errs []error, want string) bool {
	for _, err := range errs {
		if strings.Contains(err.Error(), want) {
			return true
		}
	}
	return false
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if errorsIsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			snapshot[relative+"/"] = "dir"
			return nil
		}
		// The release lock holds no data. Windows refuses to read a byte
		// range another handle has locked, and a caller may hold the lock.
		if entry.Name() == ".release.lock" {
			snapshot[relative] = "lock"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot[relative] = string(data)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return snapshot
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
